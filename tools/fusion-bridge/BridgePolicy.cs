using Lyricify.Lyrics.Models;
using Lyricify.Lyrics.Searchers;
using Lyricify.Lyrics.Searchers.Helpers;

static class BridgePolicy
{
    public static bool Accept(TrackMultiArtistMetadata track, ISearchResult candidate)
    {
        if (track.DurationMs is > 0 && candidate.DurationMs is > 0
            && Math.Abs(track.DurationMs.Value - candidate.DurationMs.Value) > 2500) return false;
        var title = CompareHelper.CompareName(track.Title, candidate.Title);
        var artist = CompareHelper.CompareArtist(track.Artists, candidate.Artists);
        if (title is not (CompareHelper.NameMatchType.Perfect or CompareHelper.NameMatchType.VeryHigh or CompareHelper.NameMatchType.High)) return false;
        if (artist is CompareHelper.ArtistMatchType.Perfect or CompareHelper.ArtistMatchType.VeryHigh or CompareHelper.ArtistMatchType.High) return true;
        var album = CompareHelper.CompareName(track.Album, candidate.Album);
        return title is CompareHelper.NameMatchType.Perfect or CompareHelper.NameMatchType.VeryHigh
            && album is CompareHelper.NameMatchType.Perfect or CompareHelper.NameMatchType.VeryHigh
            && track.DurationMs is > 0 && candidate.DurationMs is > 0
            && Cjk(track.Artist ?? "") != Cjk(candidate.Artist)
            && !string.IsNullOrWhiteSpace(track.Artist) && !string.IsNullOrWhiteSpace(candidate.Artist);
    }

    public static Response? Best(IEnumerable<Response?> responses) => responses
        .Where(response => response is not null && response.Lines.Count > 0)
        .OrderByDescending(response => response!.Lines.Count(line => !line.Unsynced) >= 2)
        .ThenByDescending(response => response!.Lines.Count(line => !line.Unsynced && !string.IsNullOrWhiteSpace(line.Translation)))
        .ThenByDescending(response => response!.Lines.Count(line => line.Words.Count > 0))
        .ThenByDescending(response => response!.Lines.Count(line => !line.Unsynced))
        .FirstOrDefault();

    static bool Cjk(string value) => value.Any(character => character is >= '\u3040' and <= '\u30ff' or >= '\u3400' and <= '\u9fff' or >= '\uac00' and <= '\ud7af');
}
