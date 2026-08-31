package duckdb

// schema is this context's own storage layout, in the same file acquisition
// uses. Every statement is idempotent, so Open can run them on a database
// that already holds acquisition's tables — or this context's own.
//
// Instants are UTC epoch milliseconds, the way acquisition stores one.
// requested_end_ms is NULL when the requested end is the literal `now`: there
// is no instant to store until a Build resolves one.
//
// ponytail: timeframes is the comma-separated canonical short forms of the
// materialized set. It is a closed vocabulary of nine tokens read and written
// whole, so a child table would buy a join and nothing else; give it one if a
// query ever needs to filter datasets by timeframe.
const schema = `
CREATE TABLE IF NOT EXISTS composite_datasets (
	name               VARCHAR PRIMARY KEY,
	instrument         VARCHAR NOT NULL,
	base_provider      VARCHAR NOT NULL,
	base_symbol        VARCHAR NOT NULL,
	base_timeframe     VARCHAR NOT NULL,
	catch_up_kind      VARCHAR NOT NULL,
	catch_up_provider  VARCHAR NOT NULL,
	catch_up_symbol    VARCHAR NOT NULL,
	catch_up_timeframe VARCHAR NOT NULL,
	requested_start_ms BIGINT  NOT NULL,
	requested_end_ms   BIGINT,
	timeframes         VARCHAR NOT NULL,
	mode               VARCHAR NOT NULL,
	state              VARCHAR NOT NULL,
	resolved_end_ms    BIGINT,
	last_error         VARCHAR DEFAULT '',
	created_at_ms      BIGINT  NOT NULL,
	updated_at_ms      BIGINT  NOT NULL
);

-- resolved_end_ms and last_error arrived after the first datasets did, so a
-- database created before them is brought up to date here rather than rebuilt.
-- Both statements are no-ops on a table that already has the column, and
-- neither carries a constraint, which is what an added column may not have.
ALTER TABLE composite_datasets ADD COLUMN IF NOT EXISTS resolved_end_ms BIGINT;
ALTER TABLE composite_datasets ADD COLUMN IF NOT EXISTS last_error VARCHAR DEFAULT '';

-- Segments are build output: recomputed and replaced wholesale by every
-- successful Build, ordered by ordinal, which is the order of the timeline.
CREATE TABLE IF NOT EXISTS composite_segments (
	dataset    VARCHAR NOT NULL,
	ordinal    INTEGER NOT NULL,
	kind       VARCHAR NOT NULL,
	instrument VARCHAR NOT NULL,
	provider   VARCHAR NOT NULL,
	symbol     VARCHAR NOT NULL,
	timeframe  VARCHAR NOT NULL,
	start_ms   BIGINT  NOT NULL,
	end_ms     BIGINT  NOT NULL,
	PRIMARY KEY (dataset, ordinal)
);

-- One Quality row per dataset: what the last Build computed. requested_end_ms
-- is NULL when the requested end was declared as now, and the available bounds
-- are NULL when the sources supplied nothing at all. The coverage percentage
-- and the open gap count are not columns: they are the expected and actual
-- counts, and the rows of composite_quality_gaps, read the obvious way.
CREATE TABLE IF NOT EXISTS composite_quality (
	dataset            VARCHAR PRIMARY KEY,
	requested_start_ms BIGINT  NOT NULL,
	requested_end_ms   BIGINT,
	resolved_end_ms    BIGINT  NOT NULL,
	available_start_ms BIGINT,
	available_end_ms   BIGINT,
	expected_bars      BIGINT  NOT NULL,
	actual_bars        BIGINT  NOT NULL,
	mode               VARCHAR NOT NULL,
	last_build_ms      BIGINT  NOT NULL
);

-- The provider boundaries that Quality records, in timeline order: where the
-- timeline changed hands, between which two sources, and the price movement
-- across the seam.
--
-- The three prices are stored as the decimal strings they are read as. A price
-- is exact in this system from acquisition's DECIMAL(20,8) column to the JSON
-- a consumer reads, and a float column here would be the one place it stopped
-- being exact. price_delta is empty when one of the two bars was not there to
-- price the transition with.
CREATE TABLE IF NOT EXISTS composite_transitions (
	dataset         VARCHAR NOT NULL,
	ordinal         INTEGER NOT NULL,
	at_ms           BIGINT  NOT NULL,
	from_instrument VARCHAR NOT NULL,
	from_provider   VARCHAR NOT NULL,
	from_symbol     VARCHAR NOT NULL,
	from_timeframe  VARCHAR NOT NULL,
	to_instrument   VARCHAR NOT NULL,
	to_provider     VARCHAR NOT NULL,
	to_symbol       VARCHAR NOT NULL,
	to_timeframe    VARCHAR NOT NULL,
	close_price     VARCHAR NOT NULL DEFAULT '',
	open_price      VARCHAR NOT NULL DEFAULT '',
	price_delta     VARCHAR NOT NULL DEFAULT '',
	PRIMARY KEY (dataset, ordinal)
);

-- The open Gaps that Quality lists, ascending. gap_id is acquisition's own, so
-- a repair can name the Gap acquisition knows.
CREATE TABLE IF NOT EXISTS composite_quality_gaps (
	dataset  VARCHAR NOT NULL,
	ordinal  INTEGER NOT NULL,
	gap_id   BIGINT  NOT NULL,
	start_ms BIGINT  NOT NULL,
	end_ms   BIGINT  NOT NULL,
	PRIMARY KEY (dataset, ordinal)
);
`
