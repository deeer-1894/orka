package service

// Quant factor pipeline — shared types.
//
// A Factor is the canonical, backtestable representation of an investment idea
// extracted from a research report: the natural-language rationale stays for
// traceability, while Expression is the machine-evaluable form that the backtest
// and portfolio layers consume. The same struct is the JSON schema the
// factor_proposer must emit, what validate_factor checks, what the backtest
// annotates, and what ingest_factor persists.

// FactorStatus is the lifecycle of a factor through the pipeline.
const (
	FactorProposed   = "proposed"   // proposer emitted it, not yet backtested
	FactorBacktested = "backtested" // has metrics, awaiting human review
	FactorApproved   = "approved"   // human approved → ingested / eligible for live
	FactorRejected   = "rejected"
	FactorLive       = "live" // in a live weighted portfolio
)

// FactorMetrics is the backtest scorecard for one factor.
type FactorMetrics struct {
	IC       float64 `json:"ic"`        // information coefficient (rank corr factor→fwd return)
	IR       float64 `json:"ir"`        // information ratio (IC mean / IC std)
	Sharpe   float64 `json:"sharpe"`    // annualized Sharpe of the factor-sorted long-short
	Turnover float64 `json:"turnover"`  // average per-period name turnover (cost proxy)
	MaxDD    float64 `json:"max_dd"`    // max drawdown of the factor portfolio
	Periods  int     `json:"periods"`   // number of backtest periods covered
}

// Factor is one extracted, (eventually) backtestable quant factor.
type Factor struct {
	FactorID       string        `json:"factor_id"`
	OwnerEmail     string        `json:"owner_email,omitempty"`
	Name           string        `json:"name"`                       // short slug/title
	SourceReportID string        `json:"source_report_id,omitempty"` // which report it came from
	Rationale      string        `json:"rationale"`                  // the NL investment logic (verbatim from the report)
	Expression     string        `json:"expression"`                 // machine-evaluable factor expression
	Direction      string        `json:"direction"`                  // long | short | long_short
	Universe       string        `json:"universe,omitempty"`         // intended stock universe
	Horizon        string        `json:"horizon,omitempty"`          // holding horizon, e.g. "1d","5d","1m"
	Status         string        `json:"status"`
	AgreementScore float64       `json:"agreement_score,omitempty"` // double-blind extraction agreement 0–1
	Metrics        FactorMetrics `json:"metrics"`
	CreatedAt      int64         `json:"created_at"`
}

// WeightedPortfolio is a combination of ingested factors with their weights and
// the combined backtest scorecard.
type WeightedPortfolio struct {
	PortfolioID string        `json:"portfolio_id"`
	OwnerEmail  string        `json:"owner_email,omitempty"`
	Method      string        `json:"method"` // equal | ic_weighted | risk_parity
	FactorIDs   []string      `json:"factor_ids"`
	Weights     []float64     `json:"weights"`
	Metrics     FactorMetrics `json:"metrics"`
	CreatedAt   int64         `json:"created_at"`
}
