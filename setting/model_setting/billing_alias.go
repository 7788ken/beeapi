package model_setting

const (
	gpt56AliasModel = "gpt-5.6"
	gpt56SolModel   = "gpt-5.6-sol"
)

// ResolveBillingModelName resolves provider aliases only for billing lookups.
// It intentionally does not rewrite the model sent upstream or token-model
// permissions.
func ResolveBillingModelName(model string) string {
	if model == gpt56AliasModel {
		return gpt56SolModel
	}
	return model
}
