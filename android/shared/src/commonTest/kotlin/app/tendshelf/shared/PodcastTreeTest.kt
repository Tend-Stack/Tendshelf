package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** A3: the podcast nodes of the car's tree, the rows they list, the row actions, and the widget's episode look. */
class PodcastTreeTest {
    @Test
    fun podcastNodesRoundTrip() {
        val nodes = listOf(Node.Podcasts, Node.PodcastNew, Node.PodcastDownloaded, Node.PodcastShows, Node.Show("ps_01H"), Node.Episode("pe_01H"))
        for (n in nodes) assertEquals(n, parseNode(nodeId(n)), nodeId(n))
    }

    @Test
    fun badPodcastIdsAreNotNodes() {
        assertNull(parseNode("show:"))
        assertNull(parseNode("show:a/b"))
        assertNull(parseNode("episode:bk_1")) // a book id is not an episode
        assertNull(parseNode("episode:pe_"))
        assertNull(parseNode("episode:pe_a b"))
        assertNull(parseNode("podcasts-other"))
    }

    private val feed = """{"items":[
        {"id":"pe_1","showId":"ps_1","title":"One","show":"Show A","durationMs":2700000,"publishedAt":5,
         "state":{"positionMs":900000,"played":false,"favourite":true,"updatedAt":9}},
        {"id":"pe_2","showId":"ps_1","title":"Gone","show":"Show A","gone":true,"state":{}},
        {"id":"pe_3","showId":"ps_1","title":"  ","show":"Show A","state":{"played":true}},
        {"id":"not_an_episode","title":"x"},
        {"id":"pe_4","showId":"ps_2","title":"Four","show":"Show B"}
    ],"next":null,"serverNow":1}"""

    @Test
    fun episodeRowsSkipWithdrawnAndBadIds() {
        val rows = PodcastJson.episodeCards(feed)
        assertEquals(listOf("pe_1", "pe_3", "pe_4"), rows.map { it.id })
        assertEquals("Episode", episodeRowTitle(rows[1])) // never a blank row
        assertEquals(true, rows[0].favourite)
        assertEquals(900_000, rows[0].positionMs)
        assertEquals(false, rows[2].played) // no state at all is unplayed
    }

    @Test
    fun sublineAndCompletion() {
        val rows = PodcastJson.episodeCards(feed)
        assertEquals("Show A, 45 min", episodeSubline(rows[0]))
        assertEquals("Show B", episodeSubline(rows[2]))
        assertEquals(1, completionOf(rows[0]))
        assertEquals(33, completionPercent(rows[0]))
        assertEquals(2, completionOf(rows[1]))
        assertEquals(0, completionOf(rows[2]))
        assertEquals("Show, 1 h 5 min", episodeSubline(rows[0].copy(show = "Show", durationMs = 3_900_000)))
    }

    @Test
    fun showRows() {
        val body = """{"items":[{"id":"ps_1","title":"A","author":"x","artUrl":"/api/v1/podcasts/art/ps_1","newCount":3},
            {"id":"ps_2","title":"","author":"","newCount":-1},{"id":"bad id","title":"z"}]}"""
        val shows = PodcastJson.showCards(body)
        assertEquals(listOf("ps_1", "ps_2"), shows.map { it.id })
        assertTrue(shows[0].hasArt)
        assertEquals(false, shows[1].hasArt)
        assertEquals(0, shows[1].newCount)
        assertEquals("Podcast", showRowTitle(shows[1]))
    }

    @Test
    fun actionsFollowTheRowsState() {
        val rows = PodcastJson.episodeCards(feed)
        assertEquals(listOf(EpisodeAction.PLAYED, EpisodeAction.UNSTAR), episodeActions(rows[0]))
        assertEquals(listOf(EpisodeAction.UNPLAYED, EpisodeAction.STAR), episodeActions(rows[1]))
        assertEquals("{\"played\":false}", episodeActionBody(EpisodeAction.UNPLAYED))
        assertEquals("{\"on\":true}", episodeActionBody(EpisodeAction.STAR))
        assertEquals(EpisodeAction.UNSTAR, episodeActionOf("unstar"))
        assertNull(episodeActionOf("download")) // not offered until A2
        assertNull(episodeActionOf("next")) // nor Play next until the queue knows episodes
    }

    @Test
    fun widgetShowsEpisodeTitleAndShow() {
        val input = WidgetInput(true, "pe_1", "The episode", "", true, 15, 45, show = "The show")
        val look = widgetLook(input, 300, 150)
        assertEquals("The episode", look.title)
        assertEquals("The show", look.chapter)
        assertTrue(look.showChapter && look.showCover)
        // The show is kept even when it repeats the title; a book's chapter is not.
        assertTrue(widgetLook(input.copy(title = "The show"), 300, 150).showChapter)
        assertEquals(false, widgetLook(input.copy(show = "", chapter = "The episode", title = "The episode"), 300, 150).showChapter)
        assertEquals(WidgetTap.RESUME_LAST, widgetPlayTap(playing = false, loaded = false))
    }

    @Test
    fun podcastUrls() {
        val p = ApiPaths("https://x.example")
        assertEquals("https://x.example/api/v1/podcasts/shows/ps_1/episodes?limit=50", p.showEpisodesUrl("ps_1", 50))
        assertEquals("https://x.example/api/v1/podcasts/episodes/pe_1/played", p.episodePlayedUrl("pe_1"))
        assertEquals("https://x.example/api/v1/podcasts/art/ps_1?w=512", p.showArtUrl("ps_1", 512))
    }
}
