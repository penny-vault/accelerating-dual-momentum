// Copyright 2021-2026
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package adm

import (
	"context"
	_ "embed"
	"fmt"
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

//go:embed README.md
var description string

type AcceleratingDualMomentum struct {
	RiskOn  universe.Universe `pvbt:"risk-on"  desc:"List of ETF, Mutual Fund, or Stock tickers to invest in" default:"VFINX,PRIDX" suggest:"Engineered Portfolio=VFINX,VINEX|PRIDX=VFINX,PRIDX|All ETF=SPY,SCZ"`
	RiskOff universe.Universe `pvbt:"risk-off" desc:"Ticker to use when model scores are all below 0"         default:"VUSTX"        suggest:"Engineered Portfolio=VUSTX|PRIDX=VUSTX|All ETF=TLT"`
}

func (s *AcceleratingDualMomentum) Name() string {
	return "Accelerating Dual Momentum"
}

func (s *AcceleratingDualMomentum) Setup(eng *engine.Engine) {
	tc, err := tradecron.New("@monthend", tradecron.MarketHours{Open: 930, Close: 1600})
	if err != nil {
		panic(err)
	}

	eng.Schedule(tc)
	eng.SetBenchmark(eng.Asset("VFINX"))
}

func (s *AcceleratingDualMomentum) Describe() engine.StrategyDescription {
	return engine.StrategyDescription{
		ShortCode:   "adm",
		Description: description,
		Source:      "https://engineeredportfolio.com/2018/05/02/accelerating-dual-momentum-investing/",
		Version:     "1.0.0",
		VersionDate: time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC),
	}
}

// riskAdjustedMomentum computes risk-adjusted momentum for a given period:
//
//	ram(n) = (price[now]/price[n_ago] - 1) * 100 - sum(dgs3mo[0:n]) / 12
func riskAdjustedMomentum(n int, prices, riskFree *data.DataFrame, dgs3moAsset asset.Asset) *data.DataFrame {
	mom := prices.Pct(n).MulScalar(100)
	rfCol := riskFree.Rolling(n).Sum().DivScalar(12).Column(dgs3moAsset, data.AdjClose)

	return mom.Apply(func(col []float64) []float64 {
		out := make([]float64, len(col))
		for i := range col {
			out[i] = col[i] - rfCol[i]
		}

		return out
	})
}

func (s *AcceleratingDualMomentum) Compute(ctx context.Context, eng *engine.Engine, strategyPortfolio portfolio.Portfolio) error {
	priceDF, err := s.RiskOn.Window(ctx, portfolio.Months(7), data.AdjClose)
	if err != nil {
		return fmt.Errorf("fetch risk-on prices: %w", err)
	}

	dgs3moAsset := eng.Asset("DGS3MO")
	dgs3moUniverse := eng.Universe(dgs3moAsset)

	riskFreeDF, err := dgs3moUniverse.Window(ctx, portfolio.Months(7), data.AdjClose)
	if err != nil {
		return fmt.Errorf("fetch DGS3MO: %w", err)
	}

	prices := priceDF.Downsample(data.Monthly).Last()
	riskFree := riskFreeDF.Downsample(data.Monthly).Last()

	log := zerolog.Ctx(ctx)
	log.Debug().
		Int("prices_rows", prices.Len()).
		Int("riskfree_rows", riskFree.Len()).
		Time("prices_start", prices.Start()).
		Time("prices_end", prices.End()).
		Msg("monthly data after downsample")

	if prices.Len() < 7 {
		return fmt.Errorf("expected at least 7 monthly price rows, got %d (start=%s end=%s)",
			prices.Len(), prices.Start().Format("2006-01-02"), prices.End().Format("2006-01-02"))
	}
	if riskFree.Len() < 7 {
		return fmt.Errorf("expected at least 7 monthly risk-free rows, got %d (start=%s end=%s)",
			riskFree.Len(), riskFree.Start().Format("2006-01-02"), riskFree.End().Format("2006-01-02"))
	}

	ram1 := riskAdjustedMomentum(1, prices, riskFree, dgs3moAsset)
	ram3 := riskAdjustedMomentum(3, prices, riskFree, dgs3moAsset)
	ram6 := riskAdjustedMomentum(6, prices, riskFree, dgs3moAsset)

	score := ram1.Add(ram3).Add(ram6).DivScalar(3)
	score = score.Drop(math.NaN()).Last()

	if score.Len() == 0 {
		return nil
	}

	for _, a := range score.AssetList() {
		r1 := ram1.Drop(math.NaN()).Last()
		r3 := ram3.Drop(math.NaN()).Last()
		r6 := ram6.Drop(math.NaN()).Last()
		log.Debug().
			Str("ticker", a.Ticker).
			Float64("mom1", r1.Value(a, data.AdjClose)).
			Float64("mom3", r3.Value(a, data.AdjClose)).
			Float64("mom6", r6.Value(a, data.AdjClose)).
			Float64("score", score.Value(a, data.AdjClose)).
			Msg("momentum score")
	}

	// Record momentum scores as structured annotations.
	score.Annotate(strategyPortfolio)

	riskOffDF, err := s.RiskOff.At(ctx, eng.CurrentDate(), data.AdjClose)
	if err != nil {
		return fmt.Errorf("fetch risk-off: %w", err)
	}

	// Select the asset with the highest positive score; fall back to risk-off.
	portfolio.MaxAboveZero(data.AdjClose, riskOffDF).Select(score)

	plan, err := portfolio.EqualWeight(score)
	if err != nil {
		return fmt.Errorf("EqualWeight: %w", err)
	}

	return strategyPortfolio.RebalanceTo(ctx, plan...)
}
