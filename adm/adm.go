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
	RiskOn  universe.Universe `pvbt:"risk-on"  desc:"List of ETF, Mutual Fund, or Stock tickers to invest in" default:"VFINX,PRIDX" suggest:"Engineered Portfolio=VFINX,VINEX|PRIDX=VFINX,PRIDX|All ETF=VOO,SCZ"`
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

func (s *AcceleratingDualMomentum) Compute(ctx context.Context, eng *engine.Engine, strategyPortfolio portfolio.Portfolio) error {
	priceDF, err := s.RiskOn.Window(ctx, portfolio.Months(7), data.AdjClose)
	if err != nil {
		return fmt.Errorf("fetch risk-on prices: %w", err)
	}

	prices := priceDF.Downsample(data.Monthly).Last()

	log := zerolog.Ctx(ctx)
	log.Debug().
		Int("prices_rows", prices.Len()).
		Time("prices_start", prices.Start()).
		Time("prices_end", prices.End()).
		Msg("monthly data after downsample")

	if prices.Len() < 7 {
		return fmt.Errorf("expected at least 7 monthly price rows, got %d (start=%s end=%s)",
			prices.Len(), prices.Start().Format("2006-01-02"), prices.End().Format("2006-01-02"))
	}

	mom1 := prices.RiskAdjustedPct(1).MulScalar(100)
	mom3 := prices.RiskAdjustedPct(3).MulScalar(100)
	mom6 := prices.RiskAdjustedPct(6).MulScalar(100)

	if mom6.Err() != nil {
		return fmt.Errorf("risk-adjusted momentum: %w", mom6.Err())
	}

	score := mom1.Add(mom3).Add(mom6).DivScalar(3)
	score = score.Drop(math.NaN()).Last()

	if score.Len() == 0 {
		return nil
	}

	for _, asset := range score.AssetList() {
		r1 := mom1.Drop(math.NaN()).Last()
		r3 := mom3.Drop(math.NaN()).Last()
		r6 := mom6.Drop(math.NaN()).Last()
		log.Debug().
			Str("ticker", asset.Ticker).
			Float64("mom1", r1.Value(asset, data.AdjClose)).
			Float64("mom3", r3.Value(asset, data.AdjClose)).
			Float64("mom6", r6.Value(asset, data.AdjClose)).
			Float64("score", score.Value(asset, data.AdjClose)).
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
