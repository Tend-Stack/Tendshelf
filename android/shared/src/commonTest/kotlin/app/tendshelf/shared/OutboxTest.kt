package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** An in-memory [OutboxStore] for the tests. */
class MemoryStore : OutboxStore {
    val rows = mutableListOf<Save>()
    private var next = 1L
    override fun last(bookId: String) = rows.lastOrNull { it.bookId == bookId }
    override fun add(s: Save): Save = s.copy(seq = next++).also { rows += it }
    override fun update(s: Save) {
        rows[rows.indexOfFirst { it.seq == s.seq }] = s
    }
    override fun firstBatch(n: Int) = rows.filter { !it.held }.take(n)
    override fun held() = rows.filter { it.held }
    override fun delete(seqs: List<Long>) {
        rows.removeAll { it.seq in seqs }
    }
    override fun trim(max: Int) {
        while (rows.size > max) {
            val i = rows.indexOfFirst { !it.finished && !it.force }
            rows.removeAt(if (i >= 0) i else 0)
        }
    }
    override fun clear() = rows.clear()
}

class OutboxTest {
    private var clock = 1_000_000L
    private val store = MemoryStore()
    private val outbox = Outbox(store) { clock }

    @Test
    fun tickMergesIntoTheLastUnsentRow() {
        outbox.tick("b", 10_000, 1.0, 10_000)
        clock += 10_000
        outbox.tick("b", 20_000, 1.0, 10_000)
        assertEquals(1, store.rows.size)
        assertEquals(20_000L, store.rows[0].positionMs)
        assertEquals(20_000L, store.rows[0].listenedMs)
    }

    @Test
    fun coalesceStopsAtFiveMinutesOfListening() {
        outbox.tick("b", 1, 1.0, 290_000)
        outbox.tick("b", 2, 1.0, 10_000) // 300,000: still merges
        assertEquals(1, store.rows.size)
        outbox.tick("b", 3, 1.0, 1) // 300,001: a new row
        assertEquals(2, store.rows.size)
    }

    @Test
    fun speedFinishedForceAndOtherBooksDoNotMerge() {
        outbox.tick("b", 1, 1.0, 1000)
        outbox.tick("b", 2, 1.5, 1000)
        assertEquals(2, store.rows.size)
        outbox.finish("b", 3, 1.5, 1000)
        outbox.tick("b", 4, 1.5, 1000) // after a finished row: new
        assertEquals(4, store.rows.size)
        outbox.force("b", 5, 1.5, 1000)
        outbox.tick("b", 6, 1.5, 1000)
        assertEquals(6, store.rows.size)
        outbox.tick("c", 1, 1.0, 1000)
        assertEquals(7, store.rows.size)
    }

    @Test
    fun clientUpdatedAtRisesWhenTheClockGoesBack() {
        val a = outbox.tick("b", 1, 1.0, 0).clientUpdatedAt
        clock -= 50_000
        val b = outbox.finish("b", 2, 1.0, 0).clientUpdatedAt
        clock -= 50_000
        val c = outbox.force("b", 3, 1.0, 0).clientUpdatedAt
        assertTrue(a < b && b < c)
    }

    @Test
    fun rowsOnTheWireAreNotChanged() {
        outbox.tick("b", 1, 1.0, 1000)
        val sent = outbox.nextBatch()
        outbox.tick("b", 2, 1.0, 1000) // new row, the one on the wire stays
        assertEquals(2, store.rows.size)
        assertEquals(1L, store.rows[0].positionMs)
        outbox.settled(sent, listOf(BatchItemResult("b", true, null, null)))
        assertEquals(listOf(2L), store.rows.map { it.positionMs })
        outbox.tick("b", 3, 1.0, 1000) // nothing on the wire now: merges
        assertEquals(1, store.rows.size)
    }

    @Test
    fun failedSendKeepsRowsAndUnfreezes() {
        outbox.tick("b", 1, 1.0, 1000)
        outbox.nextBatch()
        outbox.failed()
        outbox.tick("b", 2, 1.0, 1000)
        assertEquals(1, store.rows.size)
    }

    @Test
    fun partialAnswerDeletesOnlyWhatItCovers() {
        outbox.finish("a", 1, 1.0, 0)
        outbox.finish("b", 1, 1.0, 0)
        outbox.finish("c", 1, 1.0, 0)
        val sent = outbox.nextBatch()
        val applied = outbox.settled(sent, listOf(BatchItemResult("a", true, null, null), BatchItemResult("b", true, null, null)))
        assertEquals(listOf("c"), store.rows.map { it.bookId })
        assertEquals(2, applied.deleteSeqs.size)
    }

    @Test
    fun mismatchedAnswerDeletesNothingFromThere() {
        outbox.finish("a", 1, 1.0, 0)
        outbox.finish("b", 1, 1.0, 0)
        val sent = outbox.nextBatch()
        outbox.settled(sent, listOf(BatchItemResult("a", true, null, null), BatchItemResult("x", true, null, null)))
        assertEquals(listOf("b"), store.rows.map { it.bookId })
    }

    @Test
    fun errorCodes() {
        outbox.finish("a", 1, 1.0, 0)
        outbox.finish("b", 1, 1.0, 0)
        outbox.finish("c", 1, 1.0, 0)
        val sent = outbox.nextBatch()
        outbox.settled(
            sent,
            listOf(
                BatchItemResult("a", false, "not_found", null),
                BatchItemResult("b", false, "invalid", null),
                BatchItemResult("c", false, "internal", null),
            ),
        )
        assertEquals(listOf("c"), store.rows.map { it.bookId })
    }

    @Test
    fun notAppliedBringsTheRemotePlaceBack() {
        outbox.tick("a", 5_000, 1.0, 0)
        val sent = outbox.nextBatch()
        val remote = RemoteProgress(900_000, false, 5, "other", "Phone")
        val applied = outbox.settled(sent, listOf(BatchItemResult("a", false, null, remote)))
        assertEquals(remote, applied.lost.single().remote)
        assertTrue(store.rows.isEmpty())
    }

    @Test
    fun trimKeepsTheNewestAndFinishedRows() {
        val small = Outbox(store) { clock }
        small.finish("keep", 1, 1.0, 0)
        for (i in 1..OUTBOX_MAX_ROWS + 10) {
            clock += 1
            small.tick("book$i", i.toLong(), 1.0, 0)
        }
        assertEquals(OUTBOX_MAX_ROWS, store.rows.size)
        assertNotNull(store.rows.firstOrNull { it.bookId == "keep" })
        assertEquals("book${OUTBOX_MAX_ROWS + 10}", store.rows.last().bookId)
    }

    @Test
    fun heldRowsAreNotSentAndReleaseInTheRightWay() {
        outbox.tick("b", 1_000, 1.0, 1000, hold = true)
        outbox.tick("b", 2_000, 1.0, 1000, hold = true)
        assertEquals(1, store.rows.size)
        assertEquals(2_000L, store.rows[0].positionMs)
        assertTrue(outbox.nextBatch().isEmpty())
        val forced = outbox.release("b", force = true)!!
        assertTrue(forced.force && !forced.held)
        assertEquals(1, outbox.nextBatch().size)

        outbox.tick("c", 1, 1.0, 0, hold = true)
        outbox.dropHeld("c")
        assertNull(store.last("c"))
        outbox.tick("d", 1, 1.0, 0, hold = true)
        outbox.releaseAllHeld()
        assertTrue(!store.last("d")!!.held && !store.last("d")!!.force)
    }

    @Test
    fun newestSaveBeatsAnOlderStoredPlace() {
        val remote = RemoteProgress(5_000, false, 100, "x", "X")
        fun save(at: Long, finished: Boolean = false) = Save(1, "b", 8_000, at, finished, false, 1.0, 0, false)
        assertEquals(8_000L, startPlace(remote, null, save(200)))
        assertEquals(5_000L, startPlace(remote, null, save(50)))
        assertEquals(0L, startPlace(remote, null, save(200, finished = true)))
        assertEquals(9L, startPlace(remote, 9, save(200)))
        assertEquals(8_000L, startPlace(null, null, save(1)))
        assertEquals(200L, placeWrittenAt(remote, save(200)))
        assertNull(placeWrittenAt(null, null))
    }

    @Test
    fun batchBodyIsPlainJson() {
        val body = batchBody(listOf(Save(1, "b\"1", 5, 6, true, false, 1.25, 7, false)))
        assertEquals(
            "{\"updates\":[{\"bookId\":\"b\\\"1\",\"positionMs\":5,\"clientUpdatedAt\":6,\"finished\":true,\"force\":false,\"speed\":1.25,\"listenedMs\":7}]}",
            body,
        )
    }
}

class ConflictTest {
    private fun remote(ms: Long, device: String = "other", finished: Boolean = false, label: String = "Phone") =
        RemoteProgress(ms, finished, 1, device, label)

    @Test
    fun boundaryAtSixtySeconds() {
        assertNull(conflict("b", 100_000, remote(160_000), "me")) // exactly 60,000 apart
        assertNotNull(conflict("b", 100_000, remote(160_001), "me"))
        assertNull(conflict("b", 160_000, remote(100_000), "me"))
        assertNotNull(conflict("b", 160_001, remote(100_000), "me"))
    }

    @Test
    fun noAskForOwnDeviceServerFinishedOrNothing() {
        assertNull(conflict("b", 0, remote(900_000, device = "me"), "me"))
        assertNull(conflict("b", 0, remote(900_000, device = "server"), "me"))
        assertNull(conflict("b", 0, remote(900_000, finished = true), "me"))
        assertNull(conflict("b", 0, null, "me"))
    }

    @Test
    fun clock() {
        assertEquals("1:20", clockText(80_000))
        assertEquals("0:00", clockText(-5))
        assertEquals("1:02:03", clockText(3_723_000))
    }

    @Test
    fun theAskNamesTheDevice() {
        assertEquals(Ask("b", 900_000, "Phone"), conflict("b", 0, remote(900_000), "me"))
        assertEquals("another device", conflict("b", 0, remote(900_000, label = ""), "me")!!.deviceLabel)
    }
}

class SmartRewindTest {
    private val steps = DEFAULT_REWIND

    @Test
    fun stepEdges() {
        val cases = listOf(
            0L to 0L, 9_999L to 0L, 10_000L to 2_000L, 59_999L to 2_000L, 60_000L to 5_000L,
            3_599_999L to 5_000L, 3_600_000L to 10_000L, 86_399_999L to 10_000L, 86_400_000L to 20_000L,
            999_999_999L to 20_000L,
        )
        for ((away, want) in cases) assertEquals(want, rewindMs(away, steps, true), "away $away")
    }

    @Test
    fun offAndClamp() {
        assertEquals(0L, rewindMs(999_999_999, steps, false))
        assertEquals(0L, rewoundPosition(1_000, 100_000, steps, true))
        assertEquals(95_000L, rewoundPosition(100_000, 61_000, steps, true))
    }

    @Test
    fun aServerTableWins() {
        assertEquals(7_000L, rewindMs(5_000, listOf(RewindStep(1_000, 7)), true))
    }
}
