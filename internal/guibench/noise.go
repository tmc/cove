package guibench

import (
	"math/rand/v2"
	"strconv"
	"strings"
)

// Deterministic noise seeding (design 047 §10; AndroidWorld user_data_generation).
//
// A SQLite integrity task that ships a table with only the target row lets an
// agent pass by acting on the single item present, never demonstrating it can
// pick the right row out of a populated store. Seeding plausible distractor
// ("noise") rows forces selection. The seeding is deterministic — a seed yields
// the same rows every run — so the gold answer, the before snapshot, and the
// instruction stay self-consistent, exactly like [Task.Params] (math/rand/v2
// over a seeded PCG source, never the global rand or crypto/rand).

// NoiseRecord is one seeded distractor row. Fields holds the column values in
// table order; Row renders them in the "|"-joined whole-table form the integrity
// metrics and the snapshot getter use.
type NoiseRecord struct {
	Fields []string
}

// Row renders the record as a single "|"-joined line, matching the column form
// the snapshot getter emits (e.g. SELECT a||'|'||b FROM t) and the integrity
// metrics parse.
func (r NoiseRecord) Row() string {
	return strings.Join(r.Fields, "|")
}

// NoiseRows deterministically generates n distractor records by drawing, for
// each column, a value from the matching pool in pools. The same (seed, n,
// pools) always yields the same records, so a task's Config setup can insert
// them and the verifier's snapshot/expected value stays consistent across runs.
//
// pools is column-major: pools[c] is the candidate pool for column c, so the
// first column might be ids and the second titles. A column with an empty pool
// yields an empty value for that column. n <= 0 yields no records.
//
// NoiseRows does not deduplicate or exclude a target row: a caller that needs
// the noise set to avoid colliding with the target supplies pools that cannot
// produce it (the usual case, since the target is a fixed reserved value), and
// the integrity metric's exact multiset comparison would in any case treat a
// duplicate target faithfully.
func NoiseRows(seed uint64, n int, pools [][]string) []NoiseRecord {
	if n <= 0 || len(pools) == 0 {
		return nil
	}
	r := rand.New(rand.NewPCG(seed, noiseStream))
	out := make([]NoiseRecord, 0, n)
	for i := 0; i < n; i++ {
		fields := make([]string, len(pools))
		for c, pool := range pools {
			if len(pool) == 0 {
				fields[c] = ""
				continue
			}
			fields[c] = pool[r.IntN(len(pool))]
		}
		out = append(out, NoiseRecord{Fields: fields})
	}
	return out
}

// NoiseSQLValues renders seeded noise records as the VALUES tuples of a SQL
// INSERT, each field SQL-quoted. It is a convenience for a task's Config setup
// step, e.g.:
//
//	sqlite3 <db> "INSERT INTO notes (id,title) VALUES " + NoiseSQLValues(seed, 5, pools)
//
// Returns the empty string when no records are generated, so a caller can guard
// the INSERT. The rendered tuple order matches NoiseRecord.Fields / the column
// order the caller declared in the INSERT.
func NoiseSQLValues(seed uint64, n int, pools [][]string) string {
	records := NoiseRows(seed, n, pools)
	if len(records) == 0 {
		return ""
	}
	tuples := make([]string, len(records))
	for i, rec := range records {
		quoted := make([]string, len(rec.Fields))
		for j, f := range rec.Fields {
			quoted[j] = sqlQuote(f)
		}
		tuples[i] = "(" + strings.Join(quoted, ",") + ")"
	}
	return strings.Join(tuples, ",")
}

// noiseStream is a fixed second PCG seed lane keeping noise generation
// independent of [Task.Params]'s stream so adding noise to a task does not
// perturb its parameter draws.
const noiseStream uint64 = 0x6e6f697365 // "noise"

// sqlQuote renders s as a single-quoted SQL string literal, doubling embedded
// single quotes so a value cannot break out of the literal. Noise values come
// from controlled pools, but quoting keeps the rendered INSERT well-formed for
// any pool.
func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// noiseIntColumn renders a deterministic ascending id column as strings, a
// common need when a noise table wants stable, distinct primary keys that never
// collide with a reserved target id. ids start at start and step by one.
func noiseIntColumn(start, n int) []string {
	if n <= 0 {
		return nil
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = strconv.Itoa(start + i)
	}
	return out
}
