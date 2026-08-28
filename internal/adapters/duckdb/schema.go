package duckdb

// schema is the whole storage layout: one file, three tables, no partitioning
// (ADR-0002). Every statement is idempotent, so Open can run them on an
// existing database.
//
// open_time, start_ms and end_ms are UTC epoch milliseconds. Prices and
// volumes are DECIMAL(20,8) so a Provider's value survives storage exactly.
const schema = `
CREATE TABLE IF NOT EXISTS bars (
	provider  VARCHAR       NOT NULL,
	symbol    VARCHAR       NOT NULL,
	timeframe VARCHAR       NOT NULL,
	open_time BIGINT        NOT NULL,
	"open"    DECIMAL(20,8) NOT NULL,
	high      DECIMAL(20,8) NOT NULL,
	low       DECIMAL(20,8) NOT NULL,
	"close"   DECIMAL(20,8) NOT NULL,
	volume    DECIMAL(20,8) NOT NULL,
	PRIMARY KEY (provider, symbol, timeframe, open_time)
);

CREATE TABLE IF NOT EXISTS coverage (
	provider  VARCHAR NOT NULL,
	symbol    VARCHAR NOT NULL,
	timeframe VARCHAR NOT NULL,
	start_ms  BIGINT  NOT NULL,
	end_ms    BIGINT  NOT NULL,
	PRIMARY KEY (provider, symbol, timeframe, start_ms, end_ms)
);

CREATE SEQUENCE IF NOT EXISTS gap_ids START 1;

CREATE TABLE IF NOT EXISTS gaps (
	id        BIGINT  PRIMARY KEY DEFAULT nextval('gap_ids'),
	provider  VARCHAR NOT NULL,
	symbol    VARCHAR NOT NULL,
	timeframe VARCHAR NOT NULL,
	start_ms  BIGINT  NOT NULL,
	end_ms    BIGINT  NOT NULL,
	status    VARCHAR NOT NULL,
	reason    VARCHAR NOT NULL DEFAULT ''
);
`
