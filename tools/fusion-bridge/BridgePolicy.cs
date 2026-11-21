using Lyricify.Lyrics.Models;
using Lyricify.Lyrics.Searchers;
using Lyricify.Lyrics.Searchers.Helpers;

static class BridgePolicy
{
    private static readonly string[] AsciiVersionTokens =
    {
        "live", "remix", "acoustic", "unplugged", "instrumental", "inst",
        "karaoke", "cover", "demo", "dj", "mashup", "medley",
        "japanese", "english", "chinese", "korean", "mandarin", "cantonese",
        "sped", "slowed"
    };

    private static readonly string[] CjkVersionTags =
    {
        "伴奏", "纯音乐", "翻唱", "现场", "演唱会",
        "日语", "日文", "英文", "英语", "中文", "国语", "粤语", "韩语", "韩文",
        "吉他版", "钢琴版"
    };

    public static bool Accept(TrackMultiArtistMetadata track, ISearchResult candidate)
    {
        if (track.DurationMs is > 0 && candidate.DurationMs is > 0
            && Math.Abs(track.DurationMs.Value - candidate.DurationMs.Value) > 2500) return false;
        var title = CompareHelper.CompareName(track.Title, candidate.Title);
        var artist = CompareHelper.CompareArtist(track.Artists, candidate.Artists);
        var artistMatch = artist is CompareHelper.ArtistMatchType.Perfect or CompareHelper.ArtistMatchType.VeryHigh or CompareHelper.ArtistMatchType.High;
        var extraVersion = HasExtraVersionTag(track.Title, track.Album, candidate.Title);
        if (extraVersion) return false;

        if (title is CompareHelper.NameMatchType.Perfect or CompareHelper.NameMatchType.VeryHigh or CompareHelper.NameMatchType.High)
        {
            if (artistMatch) return true;
            var album = CompareHelper.CompareName(track.Album, candidate.Album);
            return title is CompareHelper.NameMatchType.Perfect or CompareHelper.NameMatchType.VeryHigh
                && album is CompareHelper.NameMatchType.Perfect or CompareHelper.NameMatchType.VeryHigh
                && track.DurationMs is > 0 && candidate.DurationMs is > 0
                && CrossScript(track.Artist ?? "", candidate.Artist ?? "")
                && !string.IsNullOrWhiteSpace(track.Artist) && !string.IsNullOrWhiteSpace(candidate.Artist);
        }

        if (!artistMatch) return false;
        if (BilingualParenMatch(track.Title, candidate.Title)) return true;

        return track.DurationMs is > 0 && candidate.DurationMs is > 0
            && AlbumMatches(track.Album, candidate.Album)
            && !string.IsNullOrWhiteSpace(track.Title) && !string.IsNullOrWhiteSpace(candidate.Title)
            && CrossScript(track.Title, candidate.Title);
    }

    public static Response? Best(IEnumerable<Response?> responses) => responses
        .Where(response => response is not null && response.Lines.Count > 0)
        .OrderByDescending(response => response!.Lines.Count(line => !line.Unsynced) >= 2)
        .ThenByDescending(response => response!.Lines.Count(line => !line.Unsynced && !string.IsNullOrWhiteSpace(line.Translation) && line.Translation != "//"))
        .ThenByDescending(response => response!.Lines.Count(line => line.Words.Count > 0))
        .ThenByDescending(response => response!.Lines.Count(line => !line.Unsynced))
        .FirstOrDefault();

    static bool BilingualParenMatch(string? want, string? candidate)
    {
        if (string.IsNullOrWhiteSpace(want) || string.IsNullOrWhiteSpace(candidate)) return false;
        var wantFold = Fold(Clean(want));
        if (wantFold.Length == 0) return false;
        var (outer, parens) = SplitParens(candidate);
        if (outer.Length == 0 || parens.Count == 0) return false;
        if (Fold(outer) == wantFold && parens.Any(p => CrossScript(outer, p))) return true;
        return parens.Any(p => Fold(p) == wantFold && CrossScript(outer, p));
    }

    static bool AlbumMatches(string? want, string? got)
    {
        if (string.IsNullOrWhiteSpace(want) || string.IsNullOrWhiteSpace(got)) return false;
        var direct = CompareHelper.CompareName(want, got);
        if (direct is CompareHelper.NameMatchType.Perfect or CompareHelper.NameMatchType.VeryHigh) return true;
        static IEnumerable<string> Variants(string s)
        {
            yield return Fold(s);
            yield return Fold(Clean(s));
            var idx = s.IndexOfAny(new[] { ':', '：' });
            if (idx >= 0 && idx + 1 < s.Length) yield return Fold(Clean(s[(idx + 1)..]));
        }
        var wv = Variants(want).Where(v => v.Length > 0).ToHashSet();
        return Variants(got).Any(v => v.Length > 0 && wv.Contains(v));
    }

    static bool HasExtraVersionTag(string? wantTitle, string? wantAlbum, string? candidate)
    {
        if (string.IsNullOrWhiteSpace(candidate)) return false;
        var want = $"{wantTitle} {wantAlbum}".ToLowerInvariant();
        var cand = candidate.ToLowerInvariant();
        var candTokens = Tokens(cand).ToList();
        var wantTokens = Tokens(want).ToHashSet();
        for (int i = 0; i < candTokens.Count; i++)
        {
            var tok = candTokens[i];
            if (AsciiVersionTokens.Contains(tok) && !wantTokens.Contains(tok)) return true;
            if ((tok == "ver" || tok == "version") && !wantTokens.Contains("ver") && !wantTokens.Contains("version"))
            {
                if (i == 0 || candTokens[i - 1] is not ("album" or "original" or "explicit" or "single" or "clean" or "studio"))
                    return true;
            }
        }
        return CjkVersionTags.Any(tag => cand.Contains(tag, StringComparison.Ordinal) && !want.Contains(tag, StringComparison.Ordinal));
    }

    static IEnumerable<string> Tokens(string s)
    {
        var cur = new List<char>();
        foreach (var ch in s)
        {
            if (char.IsAsciiLetterOrDigit(ch)) cur.Add(ch);
            else if (cur.Count > 0) { yield return new string(cur.ToArray()); cur.Clear(); }
        }
        if (cur.Count > 0) yield return new string(cur.ToArray());
    }

    static (string outer, List<string> parens) SplitParens(string s)
    {
        var outer = new System.Text.StringBuilder();
        var parens = new List<string>();
        var cur = new System.Text.StringBuilder();
        int depth = 0;
        foreach (var ch in s)
        {
            if (ch is '(' or '[' or '（' or '【')
            {
                if (depth == 0) cur.Clear(); else cur.Append(ch);
                depth++;
            }
            else if (ch is ')' or ']' or '）' or '】')
            {
                if (depth > 0)
                {
                    depth--;
                    if (depth == 0) { var t = cur.ToString().Trim(); if (t.Length > 0) parens.Add(t); cur.Clear(); }
                    else cur.Append(ch);
                }
            }
            else if (depth == 0) outer.Append(ch);
            else cur.Append(ch);
        }
        return (outer.ToString().Trim(), parens);
    }

    static string Clean(string s)
    {
        var dash = s.IndexOf(" - ", StringComparison.Ordinal);
        if (dash > 0) s = s[..dash];
        return SplitParens(s).outer;
    }

    static string Fold(string s) => new(s.Where(char.IsLetterOrDigit).Select(char.ToLowerInvariant).ToArray());

    static bool CrossScript(string a, string b)
    {
        static (bool latin, bool han, bool kana, bool hangul) Scan(string s)
        {
            bool latin = false, han = false, kana = false, hangul = false;
            foreach (var ch in s)
            {
                if (ch is >= '\uac00' and <= '\ud7af' or >= '\u1100' and <= '\u11ff' or >= '\u3130' and <= '\u318f') hangul = true;
                else if (ch is >= '\u3040' and <= '\u30ff') kana = true;
                else if (ch is >= '\u3400' and <= '\u9fff') han = true;
                else if (ch is >= 'A' and <= 'Z' or >= 'a' and <= 'z' or >= '\u00c0' and <= '\u024f') latin = true;
            }
            return (latin, han, kana, hangul);
        }
        var sa = Scan(a);
        var sb = Scan(b);
        var cjkA = sa.han || sa.kana || sa.hangul;
        var cjkB = sb.han || sb.kana || sb.hangul;
        if (cjkA != cjkB) return (sa.latin && !cjkA && cjkB) || (sb.latin && !cjkB && cjkA);
        if (cjkA && cjkB) return (sa.hangul != sb.hangul || sa.kana != sb.kana) && !(sa.han && sb.han);
        return false;
    }
}
