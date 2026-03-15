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

package main

import (
	"context"
	_ "embed"
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
		Description: description,
		Source:      "https://engineeredportfolio.com/2018/05/02/accelerating-dual-momentum-investing/",
		Version:     "1.1.0",
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
