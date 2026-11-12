using Lyricify.Lyrics.Models;
using Lyricify.Lyrics.Searchers;

static class BridgeChecks
{
    public static void Run()
    {
        var track = new TrackMultiArtistMetadata { Title = "桜", Artists = new() { "Yui Horie" }, Album = "水たまりに映るセカイ", DurationMs = 320626 };
        var match = new NeteaseSearchResult("桜", new[] { "堀江由衣" }, track.Album, null, 322666, "574739");
        Require(BridgePolicy.Accept(track, match), "Cross-script artist rejected despite matching title, album, and duration");
        Require(!BridgePolicy.Accept(track, new NeteaseSearchResult("桜", new[] { "Other Singer" }, track.Album, null, 322666, "wrong")), "Unrelated artist accepted");
        Require(!BridgePolicy.Accept(track, new NeteaseSearchResult("桜", new[] { "堀江由衣" }, "Other Album", null, 322666, "wrong")), "Unverified album accepted");
        Require(!BridgePolicy.Accept(track, new NeteaseSearchResult("桜", new[] { "堀江由衣" }, track.Album, null, 200000, "wrong")), "Different recording duration accepted");
        var synced = new Response { Source = "synced", Lines = new() { new() { Text = "ありがとう", At = 1000, End = 4000 }, new() { Text = "いつも", At = 4000, End = 7000 } } };
        var plain = new Response { Source = "plain", Lines = Enumerable.Range(0, 60).Select(index => new Line { Text = "Plain", Translation = "译文", Unsynced = true }).ToList() };
        var emptyYrc = new Response { Source = "credits", Lines = new() { new() { Text = "作词: Test", At = 0, End = 6000 } } };
        Require(BridgePolicy.Best(new[] { plain, emptyYrc, synced }) == synced, "Unsynced lyrics or credits outranked timed lyrics");
        var translated = new Response { Source = "translated", Lines = new() { new() { Text = "ありがとう", Translation = "谢谢你", At = 1000, End = 4000 }, new() { Text = "いつも", Translation = "一直", At = 4000, End = 7000 } } };
        Require(BridgePolicy.Best(new[] { synced, translated }) == translated, "Chinese translations were discarded");
    }

    static void Require(bool condition, string message)
    {
        if (!condition) throw new InvalidOperationException(message);
    }
}
