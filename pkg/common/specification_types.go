package common

// IATMatrix - columns are minutes, rows are IATs
type IATMatrix [][]float64

// ProbabilisticDuration used for testing the exponential distribution
type ProbabilisticDuration []float64

type GPTStats struct {
	Iterations int
	BatchSize  int
}

type RuntimeSpecification struct {
	Runtime int
	Memory  int
	Stats   GPTStats
}

type RuntimeSpecificationMatrix [][]RuntimeSpecification

type FunctionSpecification struct {
	IAT                  IATMatrix                  `json:"IAT"`
	RawDuration          ProbabilisticDuration      `json:"RawDuration"`
	RuntimeSpecification RuntimeSpecificationMatrix `json:"RuntimeSpecification"`
}
