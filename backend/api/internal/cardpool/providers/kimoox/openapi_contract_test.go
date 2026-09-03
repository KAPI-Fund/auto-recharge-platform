package kimoox

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCompatibilityOpenAPISchemaCoversDocumentedEndpoints(t *testing.T) {
	contents, err := os.ReadFile("generated/openapi.json")
	if err != nil {
		t.Fatalf("read Kimoox compatibility schema: %v", err)
	}
	var document struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("decode Kimoox compatibility schema: %v", err)
	}
	if document.OpenAPI != "3.0.3" {
		t.Fatalf("OpenAPI version = %q, want 3.0.3", document.OpenAPI)
	}

	want := map[string]string{
		"/openapi/v1/card-bins/query":               "KimooxCardBins",
		"/openapi/v1/account/balance/query":         "KimooxAccountBalance",
		"/openapi/v1/account/transactions/query":    "KimooxAccountTransactions",
		"/openapi/v1/account/convert":               "KimooxAccountConvert",
		"/openapi/v1/budget-groups/query":           "KimooxBudgetGroups",
		"/openapi/v1/budgets/create":                "KimooxBudgetsCreate",
		"/openapi/v1/budgets/query":                 "KimooxBudgetsQuery",
		"/openapi/v1/budgets/recharge":              "KimooxBudgetsRecharge",
		"/openapi/v1/budgets/withdraw":              "KimooxBudgetsWithdraw",
		"/openapi/v1/card-transactions/query":       "KimooxCardTransactions",
		"/openapi/v1/cards/funds/operate":           "KimooxCardsFundsOperate",
		"/openapi/v1/cards/status/operate":          "KimooxCardsStatusOperate",
		"/openapi/v1/cards/limit/adjust":            "KimooxCardsLimitAdjust",
		"/openapi/v1/cards/remark/update":           "KimooxCardsRemarkUpdate",
		"/openapi/v1/cards/private-info/query":      "KimooxCardsPrivateInfoQuery",
		"/openapi/v1/cards/apply":                   "KimooxCardsApply",
		"/openapi/v1/cards/apply-status/query":      "KimooxCardsApplyStatus",
		"/openapi/v1/cards/query":                   "KimooxCardsQueryOpen",
		"/openapi/v1/cards/balances/query":          "KimooxCardBalancesQuery",
		"/openapi/v1/cards/verification-code/query": "KimooxCardsVerificationCodeQuery",
		"/openapi/v1/cardholders/query":             "KimooxCardholdersQuery",
		"/openapi/v1/cardholders/create":            "KimooxCardholdersCreate",
	}
	if len(document.Paths) != len(want) {
		t.Fatalf("schema endpoint count = %d, want %d", len(document.Paths), len(want))
	}
	for path, operationID := range want {
		post, ok := document.Paths[path]["post"]
		if !ok {
			t.Fatalf("schema endpoint %s is missing POST operation", path)
		}
		if post.OperationID != operationID {
			t.Fatalf("schema endpoint %s operationId = %q, want %q", path, post.OperationID, operationID)
		}
	}
}
