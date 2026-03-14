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

2. **Fetch risk-free rate**: Get 6-month window of DGS3MO daily data via the engine.

3. **Downsample to monthly**: Convert both DataFrames from daily to monthly frequency using `.Downsample(...).Last()`.

4. **Compute risk-adjusted momentum** for each period (n = 1, 3, 6):
   - `momentum(n) = prices.Pct(n)` -- percent change over n monthly periods
   - `riskAdj(n) = dgs3mo.Rolling(n).Sum().DivScalar(12)` -- cumulative scaled risk-free rate
   - `ram(n) = momentum(n).Sub(riskAdj(n))` -- risk-adjusted momentum

5. **Average the three scores**: `score = (ram1 + ram3 + ram6) / 3`

6. **Take the last row**: current month's momentum scores for each asset.

7. **Select asset**: Use `portfolio.MaxAboveZero(data.MetricClose, riskOffDF)` -- if the best in-market momentum score is > 0, select that asset; otherwise fall back to the risk-off asset.

8. **Rebalance**: Apply `portfolio.EqualWeight()` then `p.RebalanceTo()`.

### Edge Cases

- If fewer than 7 monthly data points available after downsampling (need 6 for `Pct(6)` plus 1 base row), return early without trading.
- NaN rows produced by `Pct()` are dropped before selection.

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
