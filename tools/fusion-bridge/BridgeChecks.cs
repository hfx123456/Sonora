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

        var haru = new TrackMultiArtistMetadata { Title = "Haru Haru", Artists = new() { "BIGBANG" }, Album = "Stand Up", DurationMs = 256359 };
        Require(BridgePolicy.Accept(haru, new QQMusicSearchResult("Haru Haru (一天一天)", new[] { "BIGBANG" }, "Number 1", null, 256000, "447917", "0030QxiH38q28R")), "Bilingual parenthetical title rejected");
        Require(BridgePolicy.Accept(haru, new NeteaseSearchResult("하루하루", new[] { "BIGBANG" }, "Stand Up", null, 256360, "22673504")), "Cross-script title rejected despite matching artist, album, and duration");
        Require(!BridgePolicy.Accept(haru, new NeteaseSearchResult("Haru Haru (Japanese Version)", new[] { "BIGBANG" }, "Stand Up", null, 255773, "22673344")), "Unrequested Japanese version accepted");
        Require(!BridgePolicy.Accept(haru, new NeteaseSearchResult("하루하루", new[] { "BIGBANG" }, "Other Album", null, 256360, "wrong")), "Unverified cross-script album accepted");
        var feat = new TrackMultiArtistMetadata { Title = "Super Long Song Title (feat. Guest Artist)", Artists = new[] { "Main Artist" }.ToList(), Album = "Main Album", DurationMs = 210000 };
        Require(!BridgePolicy.Accept(feat, new NeteaseSearchResult("Super Long Song Title (feat. Guest Artist) (Live)", new[] { "Main Artist" }, "Main Album", null, 210000, "live")), "Unrequested Live version accepted despite high title similarity");
        var hanTrack = new TrackMultiArtistMetadata { Title = "一天一天", Artists = new[] { "BIGBANG" }.ToList(), Album = "Stand Up", DurationMs = 256359 };
        Require(BridgePolicy.Accept(hanTrack, new NeteaseSearchResult("하루하루", new[] { "BIGBANG" }, "Stand Up", null, 256360, "22673504")), "Han-vs-Hangul cross-script title rejected");

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
