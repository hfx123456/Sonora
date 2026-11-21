using System.Net;
using System.Text;
using System.Text.Json;
using Lyricify.Lyrics.Helpers;
using Lyricify.Lyrics.Models;
using Lyricify.Lyrics.Providers.Web;
using Lyricify.Lyrics.Searchers;
using Lyricify.Lyrics.Searchers.Helpers;
using Source = Lyricify.Lyrics.Searchers.Searchers;

Console.InputEncoding = Encoding.UTF8;
Console.OutputEncoding = new UTF8Encoding(false);
var options = new JsonSerializerOptions { PropertyNameCaseInsensitive = true, PropertyNamingPolicy = JsonNamingPolicy.CamelCase };
if (args.Contains("--self-test"))
{
    BridgeChecks.Run();
    Console.Write("matching and lyric selection passed");
    return;
}
try
{
    var request = JsonSerializer.Deserialize<Request>(await Console.In.ReadToEndAsync(), options) ?? new Request();
    using var handler = new HttpClientHandler();
    if (!string.IsNullOrEmpty(request.Proxy))
    {
        var proxyUri = request.Proxy;
        if (proxyUri.StartsWith("socks5h://", StringComparison.OrdinalIgnoreCase))
            proxyUri = "socks5://" + proxyUri["socks5h://".Length..];
        handler.Proxy = new WebProxy(proxyUri);
    }
    BaseApi.HttpClient = new HttpClient(handler) { Timeout = TimeSpan.FromSeconds(7) };
    Response? local = null;
    if (!string.IsNullOrWhiteSpace(request.Raw))
    {
        local = ConvertLyrics(request.Raw, request.Translation, "本地歌词 · Lyricify");
        if (local.Lines.Any(line => line.Translation.Length > 0)
            || (local.Lines.Any(line => !line.Unsynced) && local.Lines.Any(line => Chinese(line.Text)))
            || request.ParseOnly)
        {
            Console.Write(JsonSerializer.Serialize(local, options));
            return;
        }
    }
    var track = new TrackMultiArtistMetadata { Title = request.Title, Artists = (request.Artists ?? Array.Empty<string>()).ToList(), Album = request.Album, DurationMs = request.DurationMs };
    var tasks = new[] { Search(Source.Netease, track), Search(Source.QQMusic, track), Search(Source.LRCLIB, track) };
    var responses = await Task.WhenAll(tasks);
    var best = BridgePolicy.Best(responses.Append(local));
    Console.Write(JsonSerializer.Serialize(best ?? new Response { Error = "未找到匹配歌词，可载入本地歌词文件" }, options));
}
catch (Exception error)
{
    Console.Write(JsonSerializer.Serialize(new Response { Error = error.Message }, options));
}

static async Task<Response?> Search(Source source, TrackMultiArtistMetadata track)
{
    try
    {
        var searcher = source.GetNewSearcher();
        var queries = new[] { $"{track.Title} {track.Artist} {track.Album}", $"{track.Title} {track.Album}", $"{track.Title} {track.Artist}" }
            .Select(query => query.Trim()).Distinct();
        var batches = await Task.WhenAll(queries.Select(async query =>
        {
            try { return await searcher.SearchForResults(query).WaitAsync(TimeSpan.FromSeconds(8)) ?? new List<ISearchResult>(); }
            catch { return new List<ISearchResult>(); }
        }));
        var candidates = batches.SelectMany(batch => batch).Where(candidate => BridgePolicy.Accept(track, candidate))
            .DistinctBy(candidate => candidate switch
            {
                NeteaseSearchResult netease => netease.Id,
                QQMusicSearchResult qq => qq.Id,
                LRCLIBSearchResult lrc => lrc.Id.ToString(),
                _ => candidate.Title + candidate.Artist + candidate.Album
            })
            .OrderByDescending(candidate => CompareHelper.CompareTrack(track, candidate)).Take(3);
        return BridgePolicy.Best(await Task.WhenAll(candidates.Select(FetchLyrics)));
    }
    catch { }
    return null;
}

static async Task<Response?> FetchLyrics(ISearchResult match)
{
    try
    {
        switch (match)
        {
            case NeteaseSearchResult netease:
                var lyrics = await ProviderHelper.NeteaseApi.GetLyricNew(netease.Id).WaitAsync(TimeSpan.FromSeconds(9));
                if (lyrics == null) return null;
                return BridgePolicy.Best(new[] {
                    ConvertLyrics(lyrics.Yrc?.Lyric ?? "", First(lyrics.Ytlrc?.Lyric, lyrics.Tlyric?.Lyric), "网易云 · Lyricify"),
                    ConvertLyrics(lyrics.Lrc?.Lyric ?? "", First(lyrics.Tlyric?.Lyric, lyrics.Ytlrc?.Lyric), "网易云 · Lyricify")
                });
            case QQMusicSearchResult qq:
                var qqLyrics = await ProviderHelper.QQMusicApi.GetLyricsAsync(qq.Id).WaitAsync(TimeSpan.FromSeconds(9));
                if (qqLyrics == null) return null;
                return ConvertLyrics(qqLyrics.Lyrics ?? "", qqLyrics.Trans ?? "", "QQ 音乐 · Lyricify");
            case LRCLIBSearchResult lrc:
                var lrcLyrics = await ProviderHelper.LRCLIBApi.GetById(lrc.Id).WaitAsync(TimeSpan.FromSeconds(9));
                if (lrcLyrics == null) return null;
                return ConvertLyrics(First(lrcLyrics.SyncedLyrics, lrcLyrics.PlainLyrics), "", "LRCLIB · Lyricify");
        }
    }
    catch { }
    return null;
}

static Response ConvertLyrics(string raw, string translation, string source)
{
    var parsed = ParseHelper.ParseLyrics(raw);
    var translated = string.IsNullOrWhiteSpace(translation) ? null : ParseHelper.ParseLyrics(translation);
    var result = new Response { Source = source };
    if (parsed?.Lines == null)
    {
        if (!raw.TrimStart().StartsWith('<') && !raw.TrimStart().StartsWith('{'))
            result.Lines = raw.Split('\n').Select(text => text.Trim()).Where(text => text.Length > 0)
                .Select(text => new Line { Text = text, Unsynced = true }).ToList();
        return result;
    }
    var lines = parsed.Lines.Where(line => !string.IsNullOrWhiteSpace(line.Text)).OrderBy(line => line.StartTime ?? 0).ToList();
    for (int index = 0; index < lines.Count; index++)
    {
        var line = lines[index];
        var start = line.StartTime ?? 0;
        var nextStart = index + 1 < lines.Count ? lines[index + 1].StartTime ?? start + 6000 : start + 6000;
        var end = line.EndTime ?? Math.Min(start + 6000, nextStart);
        var chinese = (line as IFullLineInfo)?.ChineseTranslation ?? "";
        if (string.IsNullOrWhiteSpace(chinese) && line is IFullLineInfo full)
            chinese = full.Translations.FirstOrDefault(item => item.Key.StartsWith("zh", StringComparison.OrdinalIgnoreCase)).Value ?? "";
        var nearest = translated?.Lines?.Where(item => item.StartTime.HasValue
                && !string.IsNullOrWhiteSpace(item.Text) && item.Text.Trim() != "//"
                && Math.Abs(item.StartTime.Value - start) <= 500)
            .OrderBy(item => Math.Abs(item.StartTime!.Value - start)).FirstOrDefault();
        if (string.IsNullOrWhiteSpace(chinese) && nearest != null
            && !lines.Any(other => Math.Abs((other.StartTime ?? 0) - nearest.StartTime!.Value) < Math.Abs(start - nearest.StartTime!.Value)))
            chinese = nearest.Text;
        var words = line is SyllableLineInfo syllable ? syllable.Syllables.Select(word => new Word(word.Text, word.StartTime, word.EndTime)).ToList() : new List<Word>();
        result.Lines.Add(new Line { Text = line.Text, At = start, End = Math.Max(start + 1, end), Translation = chinese, Words = words, Unsynced = !line.StartTime.HasValue });
    }
    foreach (var group in result.Lines.GroupBy(line => line.At).Where(group => group.Count() == 2).ToList())
    {
        var pair = group.ToArray();
        if (!Chinese(pair[0].Text) && Chinese(pair[1].Text)) { pair[0].Translation = pair[1].Text; result.Lines.Remove(pair[1]); }
        else if (Chinese(pair[0].Text) && !Chinese(pair[1].Text)) { pair[1].Translation = pair[0].Text; result.Lines.Remove(pair[0]); }
    }
    for (int index = 0; index < result.Lines.Count; index++)
    {
        var line = result.Lines[index];
        if (index + 1 < result.Lines.Count && result.Lines[index + 1].At > line.At && line.Words.Count == 0)
            line.End = Math.Min(line.End > line.At ? line.End : line.At + 6000, result.Lines[index + 1].At);
    }
    return result;
}

static string First(params string?[] values) => values.FirstOrDefault(value => !string.IsNullOrWhiteSpace(value)) ?? "";

static bool Chinese(string text) => text.Any(character => character >= '\u4e00' && character <= '\u9fff') && !text.Any(character => character >= '\u3040' && character <= '\u30ff');

sealed class Request
{
    public string Title { get; set; } = "";
    public string[] Artists { get; set; } = Array.Empty<string>();
    public string Album { get; set; } = "";
    public int DurationMs { get; set; }
    public string Raw { get; set; } = "";
    public string Translation { get; set; } = "";
    public string Proxy { get; set; } = "";
    public bool ParseOnly { get; set; }
}
sealed class Response
{
    public string Source { get; set; } = "";
    public string Error { get; set; } = "";
    public List<Line> Lines { get; set; } = new();
}
sealed class Line
{
    public int At { get; set; }
    public int End { get; set; }
    public string Text { get; set; } = "";
    public string Translation { get; set; } = "";
    public bool Unsynced { get; set; }
    public List<Word> Words { get; set; } = new();
}
sealed record Word(string Text, int Start, int End);
