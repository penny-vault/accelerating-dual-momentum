# Accelerating Dual Momentum Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Accelerating Dual Momentum strategy as a pvbt strategy executable.

**Architecture:** Two-file Go program. `adm.go` contains the strategy struct implementing `engine.Strategy` and `engine.Descriptor`, computing risk-adjusted 6-3-1 momentum scores monthly. `main.go` is the CLI entry point. The strategy fetches 6 months of daily close prices, downsamples to monthly, computes risk-adjusted momentum for 1/3/6-month periods, averages them, and selects the best asset above zero or falls back to bonds.

**Tech Stack:** Go, github.com/penny-vault/pvbt (cli, data, engine, portfolio, tradecron, universe), github.com/rs/zerolog

**Spec:** `docs/superpowers/specs/2026-03-14-accelerating-dual-momentum-design.md`

---

## Chunk 1: Project scaffolding and strategy skeleton

### Task 1: Create main.go entry point

**Files:**
- Create: `main.go`

- [ ] **Step 1: Create main.go**

```go
package main

import "github.com/penny-vault/pvbt/cli"

func main() {
	cli.Run(&AcceleratingDualMomentum{})
}
```

- [ ] **Step 2: Commit**

```bash
git add main.go
git commit -m "Add main.go entry point"
```

### Task 2: Create adm.go with strategy struct, Name, Setup, and Describe

**Files:**
- Create: `adm.go`

- [ ] **Step 1: Create adm.go with struct, Name, Setup, Describe**

The full file is created in a single step together with the Compute method (see Task 3 for the complete code). This avoids unused import errors from a stub Compute.

- [ ] **Step 2: Commit**

```bash
git add adm.go
git commit -m "Add strategy struct with Name, Setup, and Describe"
```

### Task 3: Create complete adm.go with Compute method

**Files:**
- Create: `adm.go`

- [ ] **Step 1: Create adm.go with the full strategy implementation**

```go
package main

import (
	"context"
	"math"
	"time"

	"github.com/penny-vault/pvbt/asset"
	"github.com/penny-vault/pvbt/data"
	"github.com/penny-vault/pvbt/engine"
	"github.com/penny-vault/pvbt/portfolio"
	"github.com/penny-vault/pvbt/tradecron"
	"github.com/penny-vault/pvbt/universe"
	"github.com/rs/zerolog"
)

type AcceleratingDualMomentum struct {
	RiskOn  universe.Universe `pvbt:"risk-on"  desc:"List of ETF, Mutual Fund, or Stock tickers to invest in" default:"VFINX,PRIDX" suggest:"Engineered Portfolio=VFINX,VINEX|PRIDX=VFINX,PRIDX|All ETF=SPY,SCZ"`
	RiskOff universe.Universe `pvbt:"risk-off" desc:"Ticker to use when model scores are all below 0"         default:"VUSTX"        suggest:"Engineered Portfolio=VUSTX|PRIDX=VUSTX|All ETF=TLT"`
}

func (s *AcceleratingDualMomentum) Name() string {
	return "Accelerating Dual Momentum"
}

func (s *AcceleratingDualMomentum) Setup(e *engine.Engine) {
	tc, err := tradecron.New("@monthend", tradecron.MarketHours{Open: 930, Close: 1600})
	if err != nil {
		panic(err)
	}
	e.Schedule(tc)
	e.SetBenchmark(e.Asset("VFINX"))
	e.RiskFreeAsset(e.Asset("DGS3MO"))
}

func (s *AcceleratingDualMomentum) Describe() engine.StrategyDescription {
	return engine.StrategyDescription{
		ShortCode:   "adm",
		Description: "A market timing strategy that uses a 1-, 3-, and 6-month momentum score to select assets.",
		Source:      "https://engineeredportfolio.com/2018/05/02/accelerating-dual-momentum-investing/",
		Version:     "1.0.0",
		VersionDate: time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC),
	}
}

// riskAdjustedMomentum computes risk-adjusted momentum for a given period:
//
//	ram(n) = (price[now]/price[n_ago] - 1) * 100 - sum(dgs3mo[0:n]) / 12
//
// The risk-free column is extracted and subtracted via Apply because the
// price DataFrame and risk-free DataFrame have different asset identities.
func riskAdjustedMomentum(n int, prices, riskFree *data.DataFrame, dgs3moAsset asset.Asset) *data.DataFrame {
	mom := prices.Pct(n).MulScalar(100)
	rfCol := riskFree.Rolling(n).Sum().DivScalar(12).Column(dgs3moAsset, data.MetricClose)
	return mom.Apply(func(col []float64) []float64 {
		out := make([]float64, len(col))
		for i := range col {
			out[i] = col[i] - rfCol[i]
		}
		return out
	})
}

func (s *AcceleratingDualMomentum) Compute(ctx context.Context, e *engine.Engine, p portfolio.Portfolio) {
	log := zerolog.Ctx(ctx)

	// 1. Fetch 6-month window of daily close prices for risk-on assets.
	priceDF, err := s.RiskOn.Window(ctx, portfolio.Months(6), data.MetricClose)
	if err != nil {
		log.Error().Err(err).Msg("failed to fetch risk-on prices")
		return
	}

	// 2. Fetch 6-month window of DGS3MO (risk-free rate).
	dgs3moAsset := e.Asset("DGS3MO")
	dgs3moUniverse := e.Universe(dgs3moAsset)
	riskFreeDF, err := dgs3moUniverse.Window(ctx, portfolio.Months(6), data.MetricClose)
	if err != nil {
		log.Error().Err(err).Msg("failed to fetch DGS3MO data")
		return
	}

	// 3. Downsample both to monthly frequency (use last value in each month).
	prices := priceDF.Downsample(data.Monthly).Last()
	riskFree := riskFreeDF.Downsample(data.Monthly).Last()

	// 4. Need at least 7 monthly rows for Pct(6) and Rolling(6).Sum() to
	//    both produce at least one valid value.
	if prices.Len() < 7 {
		return
	}

	// 5. Compute risk-adjusted momentum for each period.
	ram1 := riskAdjustedMomentum(1, prices, riskFree, dgs3moAsset)
	ram3 := riskAdjustedMomentum(3, prices, riskFree, dgs3moAsset)
	ram6 := riskAdjustedMomentum(6, prices, riskFree, dgs3moAsset)

	// 6. Average the three scores and take the last row.
	score := ram1.Add(ram3).Add(ram6).DivScalar(3)
	score = score.Drop(math.NaN()).Last()

	if score.Len() == 0 {
		return
	}

	// 7. Get risk-off fallback data at the current date.
	riskOffDF, err := s.RiskOff.At(ctx, e.CurrentDate(), data.MetricClose)
	if err != nil {
		log.Error().Err(err).Msg("failed to fetch risk-off data")
		return
	}

	// 8. Select the asset with the highest positive score; fall back to risk-off.
	portfolio.MaxAboveZero(data.MetricClose, riskOffDF).Select(score)
	plan, err := portfolio.EqualWeight(score)
	if err != nil {
		log.Error().Err(err).Msg("EqualWeight failed")
		return
	}

	// 9. Rebalance to the selected asset.
	if err := p.RebalanceTo(ctx, plan...); err != nil {
		log.Error().Err(err).Msg("rebalance failed")
	}
}
```

- [ ] **Step 2: Commit**

```bash
git add adm.go
git commit -m "Add ADM strategy with Momentum631 scoring"
```

### Task 4: Add go.sum and verify build

**Files:**
- Modify: `go.mod` -- add pvbt dependency

- [ ] **Step 1: Add pvbt dependency and tidy**

```bash
cd /Users/jdf/Developer/penny-vault/strategies/accelerating-dual-momentum
go mod edit -require github.com/penny-vault/pvbt@latest
go mod tidy
```

Note: if pvbt is not published to a module proxy, use a replace directive pointing to the local copy:

```bash
go mod edit -replace github.com/penny-vault/pvbt=/Users/jdf/Developer/penny-vault/pvbt
go mod tidy
```

- [ ] **Step 2: Verify the project builds**

```bash
go build ./...
```

Expected: clean build with no errors.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "Add pvbt dependency"
```

### Task 5: Verify against reference transaction log

- [ ] **Step 1: Run a backtest from 1980 to today with default parameters**

```bash
go run . backtest --start 1980-01-01 --end 2026-03-14 --cash 10000
```

- [ ] **Step 2: Compare output transactions against ~/Downloads/export.csv**

Spot-check several key transitions from the reference log:
- 2025-03-31: should sell VFINX, buy VUSTX
- 2025-05-30: should sell VUSTX, buy PRIDX
- 2023-09-29: should sell VFINX, buy VUSTX
- 2023-11-30: should sell VUSTX, buy VFINX

If transactions don't match, debug the momentum calculation by adding temporary logging to Compute and comparing intermediate scores.

- [ ] **Step 3: Commit any fixes if needed**
