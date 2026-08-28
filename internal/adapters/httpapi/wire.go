package httpapi

import (
	"time"

	"github.com/agnos/agnoforge/internal/app"
	"github.com/agnos/agnoforge/internal/domain"
)

// This file is the whole of the wire format: every JSON document this adapter
// reads or writes, and the translation to and from the domain. Nothing else
// in the package spells a field name.
//
// Instants are RFC3339 in UTC, seconds precision, because every open_time and
// every range bound falls on a Timeframe boundary. Prices and volume stay
// decimal strings, so a DECIMAL(20,8) value never passes through a float.

// errorJSON is the body of every failed request, whatever the status.
type errorJSON struct {
	Error string `json:"error"`
}

// providerJSON describes one Provider on GET /providers.
type providerJSON struct {
	Name       string   `json:"name"`
	Timeframes []string `json:"timeframes"`
}

// rangeJSON is a half-open range [start, end).
type rangeJSON struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// datasetJSON names a Dataset by its three components.
type datasetJSON struct {
	Provider  string `json:"provider"`
	Symbol    string `json:"symbol"`
	Timeframe string `json:"timeframe"`
}

// backfillRequestJSON is the body of POST /backfills. start and end are
// RFC3339 instants or YYYY-MM-DD dates, read as UTC.
type backfillRequestJSON struct {
	Provider  string `json:"provider"`
	Symbol    string `json:"symbol"`
	Timeframe string `json:"timeframe"`
	Start     string `json:"start"`
	End       string `json:"end"`
}

// backfillStartedJSON is the 202 of POST /backfills: the id to poll and the
// range that will actually be acquired.
type backfillStartedJSON struct {
	ID             string    `json:"id"`
	EffectiveRange rangeJSON `json:"effective_range"`
}

// backfillJSON is everything observable about one Backfill. position is null
// until the first Bar lands.
type backfillJSON struct {
	ID             string      `json:"id"`
	Dataset        datasetJSON `json:"dataset"`
	Range          rangeJSON   `json:"range"`
	State          string      `json:"state"`
	BarsDownloaded int64       `json:"bars_downloaded"`
	Position       *string     `json:"position"`
	LastError      string      `json:"last_error"`
}

// gapJSON is one Gap record.
type gapJSON struct {
	ID      int64       `json:"id"`
	Dataset datasetJSON `json:"dataset"`
	Range   rangeJSON   `json:"range"`
	Status  string      `json:"status"`
	Reason  string      `json:"reason"`
}

// gapPatchJSON is the body of PATCH /gaps/{id}.
type gapPatchJSON struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// completenessJSON is the answer of GET …/complete.
type completenessJSON struct {
	Complete bool      `json:"complete"`
	Gaps     []gapJSON `json:"gaps"`
}

// repairJSON is the 202 of POST /gaps/{id}/repair.
type repairJSON struct {
	BackfillID string `json:"backfill_id"`
}

// barJSON is one Bar in the ?format=json response.
type barJSON struct {
	OpenTime string `json:"open_time"`
	Open     string `json:"open"`
	High     string `json:"high"`
	Low      string `json:"low"`
	Close    string `json:"close"`
	Volume   string `json:"volume"`
}

// asTime renders an instant the way every field of this API spells one.
func asTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func asRange(r domain.Range) rangeJSON {
	return rangeJSON{Start: asTime(r.Start), End: asTime(r.End)}
}

// asRanges renders Coverage. An empty Coverage is [], never null.
func asRanges(ranges []domain.Range) []rangeJSON {
	out := make([]rangeJSON, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, asRange(r))
	}
	return out
}

func asDataset(id domain.DatasetID) datasetJSON {
	return datasetJSON{Provider: id.Provider, Symbol: id.Symbol.String(), Timeframe: id.Timeframe.String()}
}

func asProviders(infos []app.ProviderInfo) []providerJSON {
	out := make([]providerJSON, 0, len(infos))
	for _, info := range infos {
		timeframes := make([]string, 0, len(info.Timeframes))
		for _, tf := range info.Timeframes {
			timeframes = append(timeframes, tf.String())
		}
		out = append(out, providerJSON{Name: info.Name, Timeframes: timeframes})
	}
	return out
}

// asBackfill renders a Backfill's status. Position is null while no Bar has
// landed: the zero instant is not a position.
func asBackfill(s app.BackfillStatus) backfillJSON {
	out := backfillJSON{
		ID:             string(s.ID),
		Dataset:        asDataset(s.Dataset),
		Range:          asRange(s.Range),
		State:          string(s.State),
		BarsDownloaded: s.BarsDownloaded,
		LastError:      s.LastError,
	}
	if !s.Position.IsZero() {
		position := asTime(s.Position)
		out.Position = &position
	}
	return out
}

func asGap(g domain.Gap) gapJSON {
	return gapJSON{
		ID:      g.ID,
		Dataset: asDataset(g.Dataset),
		Range:   asRange(g.Range),
		Status:  g.Status.String(),
		Reason:  g.Reason,
	}
}

// asGaps renders a list of Gaps. No Gaps is [], never null — in the body and
// in the X-Gaps header alike.
func asGaps(gaps []domain.Gap) []gapJSON {
	out := make([]gapJSON, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, asGap(g))
	}
	return out
}

func asBars(bars []domain.Bar) []barJSON {
	out := make([]barJSON, 0, len(bars))
	for _, b := range bars {
		out = append(out, barJSON{
			OpenTime: asTime(b.OpenTime),
			Open:     b.Open,
			High:     b.High,
			Low:      b.Low,
			Close:    b.Close,
			Volume:   b.Volume,
		})
	}
	return out
}
