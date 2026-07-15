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
	"strconv"
	"time"

	"github.com/penny-vault/pvbt/data"
	"github.com/penny-vault/pvbt/engine"
	"github.com/penny-vault/pvbt/portfolio"
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

func (s *AcceleratingDualMomentum) Setup(_ *engine.Engine) {}

func (s *AcceleratingDualMomentum) Describe() engine.StrategyDescription {
	return engine.StrategyDescription{
		ShortCode:   "adm",
		Description: description,
		Source:      "https://engineeredportfolio.com/2018/05/02/accelerating-dual-momentum-investing/",
		Version:     "1.0.1",
		VersionDate: time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC),
		Schedule:    "@monthend",
		Benchmark:   "SPY",
	}
}

func (s *AcceleratingDualMomentum) Compute(ctx context.Context, _ *engine.Engine, _ portfolio.Portfolio, batch *portfolio.Batch) error {
	log := zerolog.Ctx(ctx)

	priceDF, err := s.RiskOn.Window(ctx, portfolio.Months(7), data.AdjClose)
	if err != nil {
		log.Error().Err(err).Int("lookback_months", 7).Msg("failed to fetch risk-on price window")
		return fmt.Errorf("fetch risk-on price window: %w", err)
	}

	prices := priceDF.Downsample(data.Monthly).Last()

	if prices.Len() < 7 {
		log.Error().
			Int("actual_rows", prices.Len()).
			Int("required_rows", 7).
			Time("prices_start", prices.Start()).
			Time("prices_end", prices.End()).
			Msg("insufficient price history for 6-month momentum")

		return fmt.Errorf("insufficient price history: need 7 monthly observations, got %d", prices.Len())
	}

	mom1 := prices.RiskAdjustedPct(1).MulScalar(100)
	mom3 := prices.RiskAdjustedPct(3).MulScalar(100)
	mom6 := prices.RiskAdjustedPct(6).MulScalar(100)

	score := mom1.Add(mom3).Add(mom6).DivScalar(3)
	if err := score.Err(); err != nil {
		log.Error().Err(err).Msg("failed to compute composite momentum score")
		return fmt.Errorf("compute composite momentum score: %w", err)
	}

	score = score.Drop(math.NaN()).Last()
	if score.Len() == 0 {
		return nil
	}

	for _, scored := range score.AssetList() {
		momScore := score.Value(scored, data.AdjClose)
		log.Debug().
			Str("ticker", scored.Ticker).
			Float64("score", momScore).
			Msg("momentum score")

		if !math.IsNaN(momScore) {
			batch.Annotate(scored.Ticker+"/score", strconv.FormatFloat(momScore, 'f', -1, 64))
		}
	}

	riskOffDF, err := s.RiskOff.At(ctx, data.AdjClose)
	if err != nil {
		log.Error().Err(err).Msg("failed to fetch risk-off price")
		return fmt.Errorf("fetch risk-off price: %w", err)
	}

	// Select the asset with the highest positive score; fall back to risk-off.
	portfolio.MaxAboveZero(data.AdjClose, riskOffDF).Select(score)

	plan, err := portfolio.EqualWeight(score)
	if err != nil {
		log.Error().Err(err).Int("selected_assets", len(score.AssetList())).Msg("failed to build equal-weight allocation")
		return fmt.Errorf("build equal-weight allocation: %w", err)
	}

	if err := batch.RebalanceTo(ctx, plan...); err != nil {
		log.Error().Err(err).Int("allocations", len(plan)).Msg("failed to execute rebalance")
		return fmt.Errorf("rebalance to target allocation: %w", err)
	}

	return nil
}
