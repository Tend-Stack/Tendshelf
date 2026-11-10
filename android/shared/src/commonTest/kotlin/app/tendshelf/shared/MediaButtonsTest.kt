package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals

class MediaButtonsTest {
    @Test fun liveStationHasNoExtraButtons() {
        assertEquals(emptyList(), mediaButtonsFor(live = true, showSpeed = false))
        assertEquals(emptyList(), mediaButtonsFor(live = true, showSpeed = true))
    }

    @Test fun bookAndEpisodeKeepSkipButtons() {
        assertEquals(listOf(MediaButton.Back, MediaButton.Forward), mediaButtonsFor(live = false, showSpeed = false))
        assertEquals(listOf(MediaButton.Back, MediaButton.Forward, MediaButton.Speed), mediaButtonsFor(live = false, showSpeed = true))
    }
}
