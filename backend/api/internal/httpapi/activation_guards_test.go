package httpapi

import (
	"errors"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
)

func TestActivationPoolRequiresPreexistingCard(t *testing.T) {
	tests := []struct {
		name            string
		pool            models.CardPool
		wantPreexisting bool
	}{
		{
			name: "local text on demand still needs imported card",
			pool: models.CardPool{
				RoutingStrategy:  "FIXED",
				DefaultProvider:  "LOCAL_TEXT",
				CardCreationMode: "CREATE_ON_DEMAND",
			},
			wantPreexisting: true,
		},
		{
			name: "external fixed on demand can reach worker without legacy card",
			pool: models.CardPool{
				RoutingStrategy:  "FIXED",
				DefaultProvider:  "KIMOOX",
				CardCreationMode: "CREATE_ON_DEMAND",
			},
			wantPreexisting: false,
		},
		{
			name: "legacy pool switched to external default provider",
			pool: models.CardPool{
				Type:             "LOCAL",
				RoutingStrategy:  "FIXED",
				DefaultProvider:  "PHOTONPAY",
				CardCreationMode: "CREATE_ON_DEMAND",
			},
			wantPreexisting: false,
		},
		{
			name: "pool only always needs inventory",
			pool: models.CardPool{
				RoutingStrategy:  "FIXED",
				DefaultProvider:  "KIMOOX",
				CardCreationMode: "POOL_ONLY",
			},
			wantPreexisting: true,
		},
		{
			name: "fixed local wins over an unrelated external route",
			pool: models.CardPool{
				RoutingStrategy:  "FIXED",
				DefaultProvider:  "LOCAL_TEXT",
				CardCreationMode: "CREATE_ON_DEMAND",
				Providers:        []models.CardPoolProvider{{Provider: "LOCAL_TEXT", Enabled: true, Priority: 1}, {Provider: "KIMOOX", Enabled: true, Priority: 2}},
			},
			wantPreexisting: true,
		},
		{
			name: "failover external backup can issue card",
			pool: models.CardPool{
				RoutingStrategy:  "FAILOVER",
				DefaultProvider:  "LOCAL_TEXT",
				CardCreationMode: "CREATE_ON_DEMAND",
				Providers:        []models.CardPoolProvider{{Provider: "LOCAL_TEXT", Enabled: true, Priority: 1}, {Provider: "KIMOOX", Enabled: true, Priority: 2}},
			},
			wantPreexisting: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := activationPoolRequiresPreexistingCard(test.pool); got != test.wantPreexisting {
				t.Fatalf("activationPoolRequiresPreexistingCard() = %v, want %v", got, test.wantPreexisting)
			}
		})
	}
}

func TestActivationInventoryProvidersFollowPoolRouting(t *testing.T) {
	tests := []struct {
		name      string
		pool      models.CardPool
		providers []string
	}{
		{
			name: "fixed selects configured provider",
			pool: models.CardPool{
				RoutingStrategy: "FIXED", DefaultProvider: "STRIPE_ISSUING",
				Providers: []models.CardPoolProvider{
					{Provider: "LOCAL_TEXT", Enabled: true, Priority: 1},
					{Provider: "STRIPE_ISSUING", Enabled: true, Priority: 2},
				},
			},
			providers: []string{"STRIPE_ISSUING"},
		},
		{
			name: "priority selects first enabled route",
			pool: models.CardPool{
				RoutingStrategy: "PRIORITY",
				Providers: []models.CardPoolProvider{
					{Provider: "STRIPE_ISSUING", Enabled: true, Priority: 2},
					{Provider: "LOCAL_TEXT", Enabled: true, Priority: 1},
				},
			},
			providers: []string{"LOCAL_TEXT"},
		},
		{
			name: "weighted checks every enabled route",
			pool: models.CardPool{
				RoutingStrategy: "WEIGHTED",
				Providers: []models.CardPoolProvider{
					{Provider: "STRIPE_ISSUING", Enabled: true, Priority: 2},
					{Provider: "LOCAL_TEXT", Enabled: true, Priority: 1},
				},
			},
			providers: []string{"LOCAL_TEXT", "STRIPE_ISSUING"},
		},
		{
			name: "disabled route is ignored",
			pool: models.CardPool{
				RoutingStrategy: "PRIORITY",
				Providers: []models.CardPoolProvider{
					{Provider: "STRIPE_ISSUING", Enabled: false, Priority: 1},
					{Provider: "KIMOOX", Enabled: true, Priority: 2},
				},
			},
			providers: []string{"KIMOOX"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := activationInventoryProviders(test.pool)
			if strings.Join(got, ",") != strings.Join(test.providers, ",") {
				t.Fatalf("activationInventoryProviders() = %v, want %v", got, test.providers)
			}
		})
	}
}

func TestCheckActivationGuardsAllowsExternalOnDemandWithoutLegacyCard(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	server := &Server{
		DB: database,
		CardPools: cardpool.NewService(database, cardpool.NewProviderRegistry(), cardpool.NewMapConfigReader(map[string]string{
			"card_pool_default_id":         "pool-external",
			"card_pool_routing":            "FIXED",
			"card_pool_default_provider":   "KIMOOX",
			"card_pool_card_creation_mode": "CREATE_ON_DEMAND",
			"card_provider_kimoox_enabled": "1",
		})),
	}

	// The guard may read maintenance mode and the CDK, then resolve the
	// runtime-effective pool. It must not query card_assets for this route.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "app_configs"`)+`.*`).
		WithArgs("maintenance_mode", 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "is_secret"}))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "cdks"`)+`.*`).
		WithArgs("guard-external-cdk", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "code", "type", "status"}).
			AddRow("cdk-1", "guard-external-cdk", models.CDKTypeSelf, models.CDKAvailable))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "activation_attempt_limits"`)+`.*`).
		WithArgs("ip", "198.51.100.10", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "scope_type", "scope_key", "fail_count", "cooldown_until"}))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "card_pools"`)+`.*`).
		WithArgs("pool-external", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "type", "usage_type", "routing_strategy", "default_provider", "card_creation_mode", "enabled"}).
			AddRow("pool-external", "External", "EXTERNAL_API", "ONE_TIME", "FIXED", "KIMOOX", "CREATE_ON_DEMAND", true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "card_pool_providers"`) + `.*`).
		WithArgs("pool-external").
		WillReturnRows(sqlmock.NewRows([]string{"id", "pool_id", "provider", "enabled", "priority", "weight"}).
			AddRow("route-1", "pool-external", "KIMOOX", true, 1, 100))

	status, message := server.checkActivationGuards("guard-external-cdk", "browser", "198.51.100.10", "pool-external")
	if status != 0 || message != "" {
		t.Fatalf("checkActivationGuards() = (%d, %q), want (0, empty)", status, message)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestFormatActivationAdmissionFailureLogContainsResourceContextOnly(t *testing.T) {
	text := formatActivationAdmissionFailureLog("trace-card-empty", "browser", activationGuardResult{
		reason:           activationGuardReasonNoCardInventory,
		poolID:           "pool_legacy",
		provider:         "LOCAL_TEXT",
		routingStrategy:  "FIXED",
		cardCreationMode: "CREATE_ON_DEMAND",
		providers:        []string{"LOCAL_TEXT"},
	})

	for _, want := range []string{
		"trace_id=trace-card-empty",
		"pool_id=pool_legacy",
		"provider=LOCAL_TEXT",
		"routing=FIXED",
		"creation_mode=CREATE_ON_DEMAND",
		"reason=NO_AVAILABLE_CARD",
		"available_cards=0",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("formatted admission log = %q, missing %q", text, want)
		}
	}
	for _, forbidden := range []string{"Session", "accessToken", "4111111111111111", "CVC", "E2E-SECRET-CDK"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("formatted admission log contains sensitive marker %q: %q", forbidden, text)
		}
	}
}

func TestRecordActivationAdmissionFailurePersistsRuntimeLog(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("trace_id", "trace-card-empty")

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO "runtime_logs"`)).
		WithArgs(sqlmock.AnyArg(), "admission:trace-card-empty", "trace-card-empty", "warn", "activation-guard", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	server := &Server{DB: database}
	server.recordActivationAdmissionFailure(context, "browser", activationGuardResult{
		reason:           activationGuardReasonNoCardInventory,
		poolID:           "pool_legacy",
		provider:         "LOCAL_TEXT",
		routingStrategy:  "FIXED",
		cardCreationMode: "POOL_ONLY",
		providers:        []string{"LOCAL_TEXT"},
	})
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func expectActivationCapacityQuery(mock sqlmock.Sqlmock, active int64) {
	mock.ExpectExec(regexp.QuoteMeta(activationAdmissionLockSQL)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "recharge_tasks"`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(active))
}

func TestCheckActivationCapacityTxRejectsAtConfiguredDefaultLimit(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	mock.ExpectBegin()
	expectActivationCapacityQuery(mock, 1)
	mock.ExpectRollback()

	err := database.Transaction(func(tx *gorm.DB) error {
		return (&Server{}).checkActivationCapacityTx(tx)
	})
	if !errors.Is(err, errActivationCapacity) {
		t.Fatalf("capacity error = %v, want %v", err, errActivationCapacity)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestCheckActivationCapacityTxAllowsBelowLimitAndKeepsDecisionTransactional(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	mock.ExpectBegin()
	expectActivationCapacityQuery(mock, 0)
	mock.ExpectCommit()

	err := database.Transaction(func(tx *gorm.DB) error {
		return (&Server{}).checkActivationCapacityTx(tx)
	})
	if err != nil {
		t.Fatalf("capacity check error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
