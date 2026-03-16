package adm_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/penny-vault/accelerating-dual-momentum/adm"
	"github.com/penny-vault/pvbt/data"
	"github.com/penny-vault/pvbt/engine"
	"github.com/penny-vault/pvbt/portfolio"
)

var _ = Describe("AcceleratingDualMomentum", func() {
	var (
		ctx       context.Context
		snap      *data.SnapshotProvider
		nyc       *time.Location
		startDate time.Time
		endDate   time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()

		var err error
		nyc, err = time.LoadLocation("America/New_York")
		Expect(err).NotTo(HaveOccurred())

		snap, err = data.NewSnapshotProvider("testdata/snapshot.db")
		Expect(err).NotTo(HaveOccurred())

		startDate = time.Date(2024, 6, 1, 0, 0, 0, 0, nyc)
		endDate = time.Date(2026, 3, 1, 0, 0, 0, 0, nyc)
	})

	AfterEach(func() {
		if snap != nil {
			snap.Close()
		}
	})

	runBacktest := func() portfolio.Portfolio {
		strategy := &adm.AcceleratingDualMomentum{}
		acct := portfolio.New(
			portfolio.WithCash(100000, startDate),
			portfolio.WithAllMetrics(),
		)

		eng := engine.New(strategy,
			engine.WithDataProvider(snap),
			engine.WithAssetProvider(snap),
			engine.WithAccount(acct),
		)

		result, err := eng.Backtest(ctx, startDate, endDate)
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	It("produces expected returns and risk metrics", func() {
		result := runBacktest()

		summary, err := result.Summary()
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.TWRR).To(BeNumerically("~", 0.2829, 0.01))
		Expect(summary.MaxDrawdown).To(BeNumerically(">", -0.10), "max drawdown should be better than -10%")

		// Final portfolio value should reflect the total return
		Expect(result.Value()).To(BeNumerically("~", 128287, 500))
	})

	It("rotates through all three asset classes", func() {
		result := runBacktest()
		txns := result.Transactions()

		tickers := map[string]bool{}
		for _, t := range txns {
			if t.Type == portfolio.BuyTransaction || t.Type == portfolio.SellTransaction {
				tickers[t.Asset.Ticker] = true
			}
		}

		Expect(tickers).To(HaveKey("VFINX"))
		Expect(tickers).To(HaveKey("PRIDX"))
		Expect(tickers).To(HaveKey("VUSTX"))
	})

	It("produces the expected trade sequence", func() {
		result := runBacktest()
		txns := result.Transactions()

		type trade struct {
			date   string
			txType portfolio.TransactionType
			ticker string
		}

		var trades []trade
		for _, t := range txns {
			if t.Type == portfolio.BuyTransaction || t.Type == portfolio.SellTransaction {
				trades = append(trades, trade{
					date:   t.Date.In(nyc).Format("2006-01-02"),
					txType: t.Type,
					ticker: t.Asset.Ticker,
				})
			}
		}

		expected := []trade{
			{"2024-06-28", portfolio.BuyTransaction, "VFINX"},
			{"2024-09-30", portfolio.SellTransaction, "VFINX"},
			{"2024-09-30", portfolio.BuyTransaction, "PRIDX"},
			{"2024-10-31", portfolio.SellTransaction, "PRIDX"},
			{"2024-10-31", portfolio.BuyTransaction, "VFINX"},
			{"2024-12-31", portfolio.BuyTransaction, "VFINX"},
			{"2025-02-28", portfolio.SellTransaction, "VFINX"},
			{"2025-02-28", portfolio.BuyTransaction, "VUSTX"},
			{"2025-04-30", portfolio.SellTransaction, "VUSTX"},
			{"2025-04-30", portfolio.BuyTransaction, "PRIDX"},
			{"2025-07-31", portfolio.SellTransaction, "PRIDX"},
			{"2025-07-31", portfolio.BuyTransaction, "VFINX"},
			{"2025-08-29", portfolio.SellTransaction, "VFINX"},
			{"2025-08-29", portfolio.BuyTransaction, "PRIDX"},
			{"2025-09-30", portfolio.SellTransaction, "PRIDX"},
			{"2025-09-30", portfolio.BuyTransaction, "VFINX"},
			{"2025-12-31", portfolio.SellTransaction, "VFINX"},
			{"2025-12-31", portfolio.BuyTransaction, "PRIDX"},
		}

		Expect(trades).To(HaveLen(len(expected)))
		for i, exp := range expected {
			Expect(trades[i].date).To(Equal(exp.date), "trade %d date", i)
			Expect(trades[i].txType).To(Equal(exp.txType), "trade %d type", i)
			Expect(trades[i].ticker).To(Equal(exp.ticker), "trade %d ticker", i)
		}
	})
})
