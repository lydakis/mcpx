package cli

type outputMode int

const (
	outputModeText outputMode = iota
	outputModeJSON
)

func (m outputMode) isJSON() bool {
	return m == outputModeJSON
}
