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
	created_at_ms      BIGINT  NOT NULL,
	updated_at_ms      BIGINT  NOT NULL
);
`
