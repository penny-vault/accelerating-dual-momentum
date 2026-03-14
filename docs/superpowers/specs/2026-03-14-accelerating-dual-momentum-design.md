# Accelerating Dual Momentum -- Design Spec

## Overview

Implement the Accelerating Dual Momentum (ADM) strategy using the pvbt backtesting framework. ADM is a market timing strategy developed by [EngineeredPortfolio.com](https://engineeredportfolio.com/2018/05/02/accelerating-dual-momentum-investing/) that uses a Dual Momentum approach comparing absolute and relative momentum across shorter lookback periods than traditional dual momentum.

The strategy allocates 100% of the portfolio to a single asset each month, choosing between in-market equities and an out-of-market bond fund based on risk-adjusted momentum scores.

## File Structure

- `adm.go` -- strategy struct, `Setup()`, `Compute()`, `Describe()`, and momentum calculation logic
- `main.go` -- entry point, calls `cli.Run(&AcceleratingDualMomentum{})`

## Strategy Struct

```go
type AcceleratingDualMomentum struct {
    RiskOn  universe.Universe `pvbt:"risk-on"  desc:"List of ETF, Mutual Fund, or Stock tickers to invest in" default:"VFINX,PRIDX" suggest:"Engineered Portfolio=VFINX,VINEX|PRIDX=VFINX,PRIDX|All ETF=SPY,SCZ"`
    RiskOff universe.Universe `pvbt:"risk-off" desc:"Ticker to use when model scores are all below 0"         default:"VUSTX"        suggest:"Engineered Portfolio=VUSTX|PRIDX=VUSTX|All ETF=TLT"`
}
```

## Strategy Interface

### Name()

Returns `"Accelerating Dual Momentum"`.

### Setup(e *engine.Engine)

- Schedule: `@monthend` (last trading day of each month)
- Benchmark: `VFINX` (Vanguard 500 Index Fund)
- Risk-free asset: `DGS3MO` (3-Month Treasury Bill yield)

### Describe()

```go
engine.StrategyDescription{
    ShortCode:   "adm",
    Description: "A market timing strategy that uses a 1-, 3-, and 6-month momentum score to select assets.",
    Source:      "https://engineeredportfolio.com/2018/05/02/accelerating-dual-momentum-investing/",
    Version:     "1.0.0",
    VersionDate: time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC),
}
```

### Compute(ctx, e, p)

Executed on the last trading day of each month:

1. **Fetch price data**: Get 6-month window of daily close prices for risk-on assets via `s.RiskOn.Window(ctx, portfolio.Months(6), data.MetricClose)`.

2. **Fetch risk-free rate**: Create a one-asset universe for DGS3MO and fetch the same window:
   ```go
   dgs3moUniverse := e.Universe(e.Asset("DGS3MO"))
   dgs3moDF, err := dgs3moUniverse.Window(ctx, portfolio.Months(6), data.MetricClose)
   ```
   DGS3MO is an economic indicator; its values are accessed via `data.MetricClose`.

3. **Downsample to monthly**: Convert both DataFrames from daily to monthly frequency using `.Downsample(...).Last()`.

4. **Compute risk-adjusted momentum** for each period (n = 1, 3, 6):
   ```go
   momentum(n) = prices.Pct(n).MulScalar(100)  // percent change, scaled to match DGS3MO units
   rfCol       = riskFree.Rolling(n).Sum().DivScalar(12).Column(dgs3moAsset, data.MetricClose)
   ram(n)      = momentum(n).Apply(func(col []float64) []float64 {
       out := make([]float64, len(col))
       for i := range col { out[i] = col[i] - rfCol[i] }
       return out
   })
   ```
   The `.MulScalar(100)` is required because `Pct()` returns fractions (0.10 for 10%) while DGS3MO values are in percentage units (4.5 means 4.5%). The risk-free column is extracted as a `[]float64` slice and subtracted via `Apply` because `Sub` broadcast requires matching asset identities, and DGS3MO is a different asset than the risk-on equities.

5. **Average the three scores**:
   ```go
   score := ram1.Add(ram3).Add(ram6).DivScalar(3)
   ```

6. **Take the last row and select**: Call `.Last()` to get current month's scores, then use `portfolio.MaxAboveZero(data.MetricClose, riskOffDF)` to select the best in-market asset if its score > 0, otherwise fall back to the risk-off asset. The risk-off DataFrame is fetched via `s.RiskOff.At(ctx, e.CurrentDate(), data.MetricClose)`, which produces the same timestamp as `.Last()` since both are anchored to the current trading date.

7. **Rebalance**: Apply `portfolio.EqualWeight()` then `p.RebalanceTo()`.

### Edge Cases

- If fewer than 7 monthly data points available after downsampling, return early without trading. 7 rows is the minimum needed for both `Pct(6)` (first valid value at index 6) and `Rolling(6).Sum()` (first valid value at index 5) to produce at least one overlapping valid row.
- NaN rows produced by `Pct()` and `Rolling().Sum()` are dropped before selection.

## Momentum631 Formula

The core scoring formula, matching the reference implementation:

```
riskAdjustedMomentum(n) = (price[now] / price[n_months_ago] - 1) * 100 - (sum(dgs3mo[0:n]) / 12)
score = average(riskAdjustedMomentum(1), riskAdjustedMomentum(3), riskAdjustedMomentum(6))
```

Where:
- Prices are month-end close prices (downsampled from daily)
- DGS3MO is the 3-Month Treasury Bill yield (monthly)
- The risk-free rate for each period is the sum of monthly yields over that period, divided by 12

## Decision Logic

Each month:
1. Compute momentum scores for all in-market (risk-on) assets
2. Find the asset with the highest score
3. If that score > 0: invest 100% in that asset
4. If all scores <= 0: invest 100% in the risk-off asset (bonds)

## Suggested Presets

| Preset | Risk-On | Risk-Off |
|--------|---------|----------|
| Default (PRIDX) | VFINX, PRIDX | VUSTX |
| Engineered Portfolio | VFINX, VINEX | VUSTX |
| All ETF | SPY, SCZ | TLT |

## Dependencies

- `github.com/penny-vault/pvbt` (cli, data, engine, portfolio, tradecron, universe packages)
- `github.com/rs/zerolog` (logging)
