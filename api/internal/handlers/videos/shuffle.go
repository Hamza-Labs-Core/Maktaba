package videos

// Epic 56 story 56.01: the Shuffle order a play queue reads a library in.
//
// GET /api/videos?sort=shuffle&seed=N lists the videos in the order of
// md5(id || N), then id: uniform, the same for the same seed, and paged with a
// cursor like every other sort (sort=random is one page, with no cursor). The
// play queue (handlers/playqueue) stores the seed, so a queue reloads in the same
// order, and a new seed is "Shuffle again".

import (
	"crypto/md5" //nolint:gosec // an order, not a secret
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"strconv"

	"modernc.org/sqlite"
)

// sortShuffle is GET /api/videos?sort=shuffle (it needs seed).
const sortShuffle = "shuffle"

func init() {
	// SQLite (the desktop single-user build) has no md5(): the shuffle sort's
	// expression is the same on both databases, so the driver gets one.
	if err := sqlite.RegisterDeterministicScalarFunction("md5", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if args[0] == nil {
			return nil, nil
		}
		var b []byte
		switch v := args[0].(type) {
		case string:
			b = []byte(v)
		case []byte:
			b = v
		default:
			b = []byte(fmt.Sprint(v))
		}
		sum := md5.Sum(b) //nolint:gosec // an order, not a secret
		return hex.EncodeToString(sum[:]), nil
	}); err != nil {
		panic("videos: register md5 with sqlite: " + err.Error())
	}
}

// ShuffleKey is the key a video sorts by in the shuffle of seed: md5 of the id
// and the seed in decimal, in hex (the SQL expression's value).
func ShuffleKey(id string, seed int64) string {
	sum := md5.Sum([]byte(id + strconv.FormatInt(seed, 10))) //nolint:gosec // an order, not a secret
	return hex.EncodeToString(sum[:])
}

// shuffleSort is the listSort of the shuffle of seed. The seed is a number
// written into the SQL, never text from the request.
func shuffleSort(seed int64) listSort {
	return listSort{expr: "md5(CAST(id AS TEXT) || '" + strconv.FormatInt(seed, 10) + "')", kind: keyText}
}

// sortOf is the listSort of a sort name (the shuffle's for seed).
func sortOf(sort string, seed int64) listSort {
	if sort == sortShuffle {
		return shuffleSort(seed)
	}
	return listSorts[sort]
}
