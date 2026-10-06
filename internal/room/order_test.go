package room

import (
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
)

// The order rules are pure: these tests need no database.

// queueOf builds items named by Rank (only used to read results here),
// added a minute apart in the given order.
func queueOf(names ...string) []*entity.QueueItem {
	t0 := time.Unix(1_800_000_000, 0)
	out := make([]*entity.QueueItem, len(names))
	for i, n := range names {
		out[i] = &entity.QueueItem{ID: uuid.New(), Rank: n, CreatedAt: t0.Add(time.Duration(i) * time.Minute)}
	}
	return out
}

func names(q []*entity.QueueItem) []string {
	out := make([]string, len(q))
	for i, it := range q {
		out[i] = it.Rank
	}
	return out
}

func TestVoteOrder(t *testing.T) {
	q := queueOf("a", "b", "c", "d")
	q[1].Votes, q[2].Votes, q[3].Votes = 1, 3, 1
	before := names(q)

	// Most votes first, ties by age; the current item leads wherever it was.
	assert.Equal(t, []string{"b", "c", "d", "a"}, names(voteOrder(q, &q[1].ID)))
	assert.Equal(t, []string{"c", "b", "d", "a"}, names(voteOrder(q, nil)))
	assert.Equal(t, before, names(q), "the input is not reordered")
}

func TestFairOrder(t *testing.T) {
	ann, bob, cat := uuid.New(), uuid.New(), uuid.New()
	q := queueOf("cur", "a1", "a2", "a3", "b1", "c1", "anon")
	q[0].AddedBy = &ann
	for i, who := range []*uuid.UUID{&ann, &ann, &ann, &bob, &cat, nil} {
		q[i+1].AddedBy = who
	}

	// Turns in first-come order, starting after the current video's adder
	// (Ann), whose next video waits for everyone else.
	assert.Equal(t, []string{"cur", "b1", "c1", "anon", "a1", "a2", "a3"}, names(fairOrder(q, &q[0].ID)))
	// Nothing playing: "cur" is just Ann's first video, so she starts and
	// every round takes one from each person.
	assert.Equal(t, []string{"cur", "b1", "c1", "anon", "a1", "a2", "a3"}, names(fairOrder(q, nil)))

	// Fewer than two waiting items: nobody to take turns with, so the order
	// stays, even with the current item not at the head (after a jump).
	q = queueOf("x", "cur")
	assert.Equal(t, []string{"x", "cur"}, names(fairOrder(q, &q[1].ID)))
}

func TestPlaceAfter(t *testing.T) {
	q := queueOf("cur", "a", "b", "c")
	cur := &q[0].ID

	got, err := placeAfter(q, 3, &q[1].ID, cur)
	require.NoError(t, err)
	assert.Equal(t, []string{"cur", "a", "c", "b"}, names(got))

	// The head means right after the current item, never before it.
	got, err = placeAfter(q, 2, nil, cur)
	require.NoError(t, err)
	assert.Equal(t, []string{"cur", "b", "a", "c"}, names(got))
	got, err = placeAfter(q, 2, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "cur", "a", "c"}, names(got))

	missing := uuid.New()
	_, err = placeAfter(q, 1, &missing, cur)
	var re *Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, "anchor item not found", re.Message)
	assert.Equal(t, []string{"cur", "a", "b", "c"}, names(q), "the input is not reordered")
}

func TestBatchAfterCurrent(t *testing.T) {
	q := queueOf("a", "cur", "b")
	batch := queueOf("p1", "p2")
	assert.Equal(t, []string{"cur", "p1", "p2", "a", "b"}, names(batchAfterCurrent(q, batch, &q[1].ID)))
}

func TestShuffled(t *testing.T) {
	reverse := func(n int, swap func(i, j int)) {
		for i := 0; i < n/2; i++ {
			swap(i, n-1-i)
		}
	}
	q := queueOf("cur", "a", "b", "c")
	assert.Equal(t, []string{"cur", "c", "b", "a"}, names(shuffled(q, &q[0].ID, reverse)))
	assert.Equal(t, []string{"c", "b", "a", "cur"}, names(shuffled(q, nil, reverse)))
	assert.Equal(t, []string{"cur", "a", "b", "c"}, names(q), "the input is not reordered")
}

func TestFollowing(t *testing.T) {
	q := queueOf("a", "b")
	assert.Equal(t, "b", following(q, 1).Rank)
	assert.Equal(t, "a", following(q, 2).Rank, "past the end: back to the head")
	assert.Equal(t, "a", following(q, -1).Rank)
	assert.Nil(t, following(nil, 0))
}
