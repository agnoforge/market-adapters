// Package domain holds the Composite Market Dataset model: the Composite
// Dataset itself, its declared configuration (Instrument, base and catch-up
// Sources, requested range, materialized Timeframes, Mode) and its lifecycle
// State.
//
// It depends only on the standard library and on the acquisition domain for
// the terms that context owns — Range, Symbol — which this context uses with
// acquisition's meaning (see CONTEXT-MAP.md). It knows nothing about storage,
// HTTP or the acquisition application service.
package domain
