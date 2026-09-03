package db

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func Open(databaseURL string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
}

func Migrate(database *gorm.DB) error {
	if err := prepareRechargeTaskPaymentRegion(database); err != nil {
		return err
	}
	if err := prepareRechargeTaskKeys(database); err != nil {
		return err
	}
	if err := prepareRechargeTaskCDKRelation(database); err != nil {
		return err
	}
	if err := prepareStoreOrderKeys(database); err != nil {
		return err
	}
	if err := preparePlanInventory(database); err != nil {
		return err
	}
	if err := prepareCardTransactionIndex(database); err != nil {
		return err
	}
	if err := preparePaymentCardIndex(database); err != nil {
		return err
	}
	if err := prepareCardAllocationReleaseClaims(database); err != nil {
		return err
	}
	if err := purgeLegacyBillingCardNumber(database); err != nil {
		return err
	}
	if err := database.AutoMigrate(
		&models.Plan{},
		&models.StoreOrder{},
		&models.StorePaymentEvent{},
		&models.EmailDelivery{},
		&models.CDK{},
		&models.RechargeTask{},
		&models.ProductGenerationTask{},
		&models.PhoneAsset{},
		&models.CardAsset{},
		&models.CardPool{},
		&models.CardPoolProvider{},
		&models.PaymentCard{},
		&models.CardProvisionRequest{},
		&models.CardAllocation{},
		&models.CardProviderEvent{},
		&models.CardTransaction{},
		&models.BillingRecord{},
		&models.AppConfig{},
		&models.ActivationAttemptLimit{},
		&models.ProductAsset{},
		&models.PoolEmail{},
		&models.TaxFreeAddress{},
		&models.ProxyAsset{},
		&models.AdminLoginLog{},
		&models.RuntimeLog{},
	); err != nil {
		return err
	}
	if err := normalizePlanCurrencies(database); err != nil {
		return err
	}
	if err := seedPlans(database); err != nil {
		return err
	}
	if err := seedCardPools(database); err != nil {
		return err
	}
	return seedTaxFreeAddresses(database)
}

// Older installations do not have a task-level payment region. Add it before
// AutoMigrate so upgrades are repeatable and legacy rows remain readable.
func prepareRechargeTaskPaymentRegion(database *gorm.DB) error {
	if database == nil || !database.Migrator().HasTable(&models.RechargeTask{}) {
		return nil
	}
	if err := database.Exec(`ALTER TABLE "recharge_tasks" ADD COLUMN IF NOT EXISTS "payment_region" varchar(4) NOT NULL DEFAULT ''`).Error; err != nil {
		return fmt.Errorf("prepare recharge task payment region: %w", err)
	}
	return nil
}

// Older installations may already have card_allocations without the
// short-lived Provider release claim columns. Add them before AutoMigrate so
// recovery can safely run during the same startup that upgrades the schema.
func prepareCardAllocationReleaseClaims(database *gorm.DB) error {
	if database == nil || !database.Migrator().HasTable(&models.CardAllocation{}) {
		return nil
	}
	if err := database.Exec(`ALTER TABLE "card_allocations" ADD COLUMN IF NOT EXISTS "provider_release_claim_token" varchar(96)`).Error; err != nil {
		return fmt.Errorf("prepare card allocation release claim token: %w", err)
	}
	if err := database.Exec(`ALTER TABLE "card_allocations" ADD COLUMN IF NOT EXISTS "provider_release_claimed_at" timestamptz`).Error; err != nil {
		return fmt.Errorf("prepare card allocation release claim timestamp: %w", err)
	}
	return nil
}

// Older databases predate storefront inventory. Add the columns explicitly
// before AutoMigrate so existing plans get safe zero values and the migration
// is repeatable on both fresh and upgraded installations.
func preparePlanInventory(database *gorm.DB) error {
	if database == nil || !database.Migrator().HasTable(&models.Plan{}) {
		return nil
	}
	if err := database.Exec(`ALTER TABLE "plans" ADD COLUMN IF NOT EXISTS "sale_limit" integer NOT NULL DEFAULT 0`).Error; err != nil {
		return fmt.Errorf("prepare plan sale limit: %w", err)
	}
	if err := database.Exec(`ALTER TABLE "plans" ADD COLUMN IF NOT EXISTS "sold_count" integer NOT NULL DEFAULT 0`).Error; err != nil {
		return fmt.Errorf("prepare plan sold count: %w", err)
	}
	if err := database.Exec(`UPDATE "plans" SET "sale_limit" = 0 WHERE "sale_limit" IS NULL`).Error; err != nil {
		return fmt.Errorf("backfill plan sale limit: %w", err)
	}
	if err := database.Exec(`UPDATE "plans" SET "sold_count" = 0 WHERE "sold_count" IS NULL`).Error; err != nil {
		return fmt.Errorf("backfill plan sold count: %w", err)
	}
	return nil
}

// Older builds persisted a full card number in billing_records.card_number.
// The current model deliberately has no such field, so remove the legacy
// column before AutoMigrate. This is a one-way security migration: billing
// history retains only card_last4 and never needs the PAN to function.
func purgeLegacyBillingCardNumber(database *gorm.DB) error {
	if database == nil || !database.Migrator().HasTable("billing_records") {
		return nil
	}
	if err := database.Exec(`ALTER TABLE "billing_records" DROP COLUMN IF EXISTS "card_number"`).Error; err != nil {
		return fmt.Errorf("purge legacy billing card number: %w", err)
	}
	return nil
}

// Older builds created a globally unique transaction ID index. Provider
// transaction IDs are only unique within a provider, so replace that index
// before AutoMigrate creates the composite constraint used by CardTransaction.
func prepareCardTransactionIndex(database *gorm.DB) error {
	if database == nil || !database.Migrator().HasTable(&models.CardTransaction{}) {
		return nil
	}
	columns, exists, err := findIndexColumns(database, &models.CardTransaction{}, "ux_card_transactions_provider_id")
	if err != nil {
		return fmt.Errorf("inspect card transaction provider index: %w", err)
	}
	if !exists || sameIndexColumns(columns, "provider", "provider_transaction_id") {
		return nil
	}
	if err := database.Migrator().DropIndex(&models.CardTransaction{}, "ux_card_transactions_provider_id"); err != nil {
		return fmt.Errorf("replace card transaction provider index: %w", err)
	}
	return nil
}

// Older builds created a globally unique payment-card index on provider_card_id
// alone. Provider card IDs are only unique within their provider, so keep the
// index name stable while correcting its columns before AutoMigrate runs.
func preparePaymentCardIndex(database *gorm.DB) error {
	if database == nil || !database.Migrator().HasTable(&models.PaymentCard{}) {
		return nil
	}
	columns, exists, err := findIndexColumns(database, &models.PaymentCard{}, "ux_payment_cards_provider_card")
	if err != nil {
		return fmt.Errorf("inspect payment card provider index: %w", err)
	}
	if !exists || sameIndexColumns(columns, "provider", "provider_card_id") {
		return nil
	}
	if err := database.Migrator().DropIndex(&models.PaymentCard{}, "ux_payment_cards_provider_card"); err != nil {
		return fmt.Errorf("replace payment card provider index: %w", err)
	}
	return nil
}

func findIndexColumns(database *gorm.DB, model any, name string) ([]string, bool, error) {
	indexes, err := database.Migrator().GetIndexes(model)
	if err != nil {
		return nil, false, err
	}
	for _, index := range indexes {
		if index.Name() != name {
			continue
		}
		return index.Columns(), true, nil
	}
	return nil, false, nil
}

func sameIndexColumns(actual []string, expected ...string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index, column := range actual {
		if strings.ToLower(strings.TrimSpace(column)) != strings.ToLower(strings.TrimSpace(expected[index])) {
			return false
		}
	}
	return true
}

func normalizePlanCurrencies(database *gorm.DB) error {
	if err := database.Model(&models.Plan{}).Where("currency <> ?", models.PlatformStoreCurrency).Update("currency", models.PlatformStoreCurrency).Error; err != nil {
		return fmt.Errorf("normalize platform store currencies: %w", err)
	}
	return nil
}

var providerPlanNames = map[string]string{
	"plus":            "chatgptplusplan",
	"pro_5x":          "chatgptprolite",
	"pro_20x":         "chatgptpro",
	"go":              "chatgptgoplan",
	"chatgptgoplan":   "chatgptgoplan",
	"chatgptplusplan": "chatgptplusplan",
	"chatgptprolite":  "chatgptprolite",
	"chatgptpro":      "chatgptpro",
}

// ProviderPlanNameForCode is the single source of truth for the plan_name
// values expected by the original ChatGPT Checkout API.
func ProviderPlanNameForCode(code string) string {
	normalized := strings.ToLower(strings.TrimSpace(code))
	if name, ok := providerPlanNames[normalized]; ok {
		return name
	}
	return strings.TrimSpace(code)
}

// NormalizeProviderPlanName repairs legacy aliases while preserving an
// explicitly supplied custom value for the original manual override field.
func NormalizeProviderPlanName(planType, value string) string {
	if strings.TrimSpace(value) != "" {
		if name, ok := providerPlanNames[strings.ToLower(strings.TrimSpace(value))]; ok {
			return name
		}
		return strings.TrimSpace(value)
	}
	return ProviderPlanNameForCode(planType)
}

// PlanTypeForPlan returns the legacy worker's bounded plan_type value for a
// platform plan. Storefront product codes are intentionally allowed to be
// custom and up to 64 characters, while CDK.plan_type is the business plan
// selector consumed by the original worker and only has room for 24. Prefer
// the linked canonical provider plan so a custom product still runs the
// correct Plus/Pro flow instead of leaking its display code into that field.
func PlanTypeForPlan(plan models.Plan) string {
	for _, value := range []string{plan.ProviderPlanName, plan.Code} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "chatgptplusplan", "plus":
			return "plus"
		case "chatgptprolite", "pro_5x", "pro5x":
			return "pro_5x"
		case "chatgptpro", "pro_20x", "pro20x":
			return "pro_20x"
		case "chatgptgoplan", "go", "chatgpt_go":
			return "go"
		}
	}
	code := strings.TrimSpace(plan.Code)
	if len(code) <= 24 {
		return code
	}
	// A legacy/custom plan without a recognized provider mapping cannot be
	// represented safely in the worker contract. Keep the historical fallback
	// rather than failing a paid order after inventory has been consumed.
	return "plus"
}

func prepareStoreOrderKeys(database *gorm.DB) error {
	if !database.Migrator().HasTable(&models.StoreOrder{}) {
		return nil
	}
	if err := database.Exec(`ALTER TABLE "store_orders" ADD COLUMN IF NOT EXISTS "order_no" varchar(64)`).Error; err != nil {
		return fmt.Errorf("prepare store order order_no: %w", err)
	}
	if err := database.Exec(`UPDATE "store_orders" SET "order_no" = CONCAT('ORD-', "id") WHERE "order_no" IS NULL OR "order_no" = ''`).Error; err != nil {
		return fmt.Errorf("backfill store order order_no: %w", err)
	}
	return nil
}

// Older local databases predate job_key. Add and backfill it before GORM
// applies the current NOT NULL and unique-index constraints.
func prepareRechargeTaskKeys(database *gorm.DB) error {
	if !database.Migrator().HasTable(&models.RechargeTask{}) {
		return nil
	}
	if err := database.Exec(`ALTER TABLE "recharge_tasks" ADD COLUMN IF NOT EXISTS "job_key" varchar(96)`).Error; err != nil {
		return fmt.Errorf("prepare recharge task job_key: %w", err)
	}
	if err := database.Exec(`UPDATE "recharge_tasks" SET "job_key" = CONCAT('legacy-', "id") WHERE "job_key" IS NULL OR "job_key" = ''`).Error; err != nil {
		return fmt.Errorf("backfill recharge task job_key: %w", err)
	}
	return nil
}

// Checkout debugging creates a worker task without consuming a customer CDK.
// Older schemas made cdk_id mandatory, so normalize legacy empty sentinels and
// make the relationship nullable before AutoMigrate applies the model.
func prepareRechargeTaskCDKRelation(database *gorm.DB) error {
	if !database.Migrator().HasTable(&models.RechargeTask{}) || !database.Migrator().HasColumn(&models.RechargeTask{}, "cdk_id") {
		return nil
	}
	if err := database.Exec(`UPDATE "recharge_tasks" SET "cdk_id" = NULL WHERE "cdk_id" = ''`).Error; err != nil {
		return fmt.Errorf("normalize empty recharge task cdk ids: %w", err)
	}
	if err := database.Exec(`ALTER TABLE "recharge_tasks" ALTER COLUMN "cdk_id" DROP NOT NULL`).Error; err != nil {
		return fmt.Errorf("allow nullable recharge task cdk id: %w", err)
	}
	return nil
}

func seedPlans(database *gorm.DB) error {
	plans := []models.Plan{
		{ID: "plan_plus", Code: "plus", Name: "ChatGPT Plus", Description: "适合日常使用：GPT-5.5、高级数据分析、DALL·E 等 Plus 权益，按月订阅。", ProviderPlanName: ProviderPlanNameForCode("plus"), Country: "US", Currency: models.PlatformStoreCurrency, Price: 20, SortOrder: 10, Active: true},
		{ID: "plan_pro_5x", Code: "pro_5x", Name: "Pro 5x", Description: "在 Pro 基础上提供约 5 倍的消息/推理额度，适合重度个人用户与创作者。", ProviderPlanName: ProviderPlanNameForCode("pro_5x"), Country: "US", Currency: models.PlatformStoreCurrency, Price: 100, SortOrder: 20, Active: true},
		{ID: "plan_pro_20x", Code: "pro_20x", Name: "Pro 20x", Description: "最高约 20 倍 Pro 用量上限，适合团队主力账号、开发测试与高并发场景。", ProviderPlanName: ProviderPlanNameForCode("pro_20x"), Country: "US", Currency: models.PlatformStoreCurrency, Price: 200, SortOrder: 30, Active: true},
		{ID: "plan_go", Code: "go", Name: "ChatGPT Go", Description: "适合需要基础高级功能的轻量套餐。", ProviderPlanName: ProviderPlanNameForCode("go"), Country: "IN", Currency: models.PlatformStoreCurrency, Price: 10, SortOrder: 40, Active: true},
	}
	for _, plan := range plans {
		var existing models.Plan
		result := database.Where("code = ?", plan.Code).First(&existing)
		if result.Error == nil {
			updates := map[string]any{}
			if existing.Currency != models.PlatformStoreCurrency {
				updates["currency"] = models.PlatformStoreCurrency
			}
			if existing.ProviderPlanName != plan.ProviderPlanName {
				updates["provider_plan_name"] = plan.ProviderPlanName
			}
			legacyDescriptions := map[string]string{
				"plus":    "适合日常使用的标准订阅套餐。",
				"pro_5x":  "面向高频使用者的高用量套餐。",
				"pro_20x": "面向团队和重度场景的旗舰套餐。",
			}
			if existing.Description == legacyDescriptions[plan.Code] || strings.TrimSpace(existing.Description) == "" {
				updates["description"] = plan.Description
			}
			if len(updates) > 0 {
				if err := database.Model(&existing).Updates(updates).Error; err != nil {
					return fmt.Errorf("update plan %s defaults: %w", plan.Code, err)
				}
			}
			continue
		}
		if result.Error != gorm.ErrRecordNotFound {
			return result.Error
		}
		if err := database.Create(&plan).Error; err != nil {
			return fmt.Errorf("seed plan %s: %w", plan.Code, err)
		}
	}
	defaults := map[string]string{
		"mode":                                     "browser",
		"payment_region":                           "PH",
		"admin_email":                              "admin@example.com",
		"admin_password_version":                   "1",
		"admin_secondary_password_version":         "1",
		"admin_totp_enabled":                       "0",
		"admin_2fa_login_mode":                     "either",
		"telegram_on_admin_login":                  "1",
		"admin_login_path":                         "/admin-login",
		"admin_panel_path":                         "/admin",
		"max_concurrent_activations":               "1",
		"max_background_concurrent":                "1",
		"recharge_queued_timeout_seconds":          "600",
		"recharge_task_lease_timeout_seconds":      "60",
		"maintenance_mode":                         "0",
		"maintenance_mode_drain":                   "0",
		"browser_pool_enabled":                     "1",
		"pool_email_enabled":                       "0",
		"pool_email_imap_host":                     "outlook.office365.com",
		"pool_email_imap_port":                     "993",
		"pool_email_include_junk":                  "1",
		"email_source":                             "random",
		"random_email_domain":                      "chiyiyi.cloud",
		"inbox_api_base":                           "https://temp-email-api.jzqkwl.com",
		"upstreamCreatePath":                       "/pay",
		"upstreamStatusPath":                       "/tasks/:id",
		"gpt_api_enabled":                          "0",
		"gpt_api_base_url":                         "https://kc.vpss.eu.cc/",
		"gpt_api_plan_key":                         "plus",
		"gpt_api_country":                          "PH",
		"gpt_api_currency":                         "PHP",
		"hcaptcha_solver_enabled":                  "1",
		"hcaptcha_vlm_base_url":                    "https://api.openai.com/v1",
		"hcaptcha_vlm_model":                       "gpt-5.5",
		"hcaptcha_vlm_timeout":                     "45",
		"hcaptcha_solver_timeout":                  "240",
		"hcaptcha_solver_no_vlm":                   "0",
		"hcaptcha_cdp_port":                        "9222",
		"card_pool_default_id":                     "pool_legacy",
		"card_pool_routing":                        "FIXED",
		"card_pool_default_provider":               "LOCAL_TEXT",
		"card_pool_card_creation_mode":             "CREATE_ON_DEMAND",
		"card_provider_local_text_enabled":         "1",
		"card_provider_airwallex_enabled":          "0",
		"card_provider_stripe_issuing_enabled":     "0",
		"card_provider_photonpay_enabled":          "0",
		"card_provider_dogpay_enabled":             "0",
		"card_provider_kimoox_enabled":             "0",
		"airwallex_base_url":                       "https://api.sandbox.airwallex.com",
		"airwallex_form_factor":                    "VIRTUAL",
		"airwallex_activate_on_issue":              "1",
		"airwallex_primary_currency":               "USD",
		"airwallex_card_purpose":                   "COMMERCIAL",
		"airwallex_card_type":                      "DEBIT",
		"airwallex_created_by":                     "auto-recharge-platform",
		"airwallex_webhook_tolerance_seconds":      "300",
		"stripe_issuing_base_url":                  "https://api.stripe.com",
		"stripe_issuing_currency":                  "USD",
		"stripe_issuing_webhook_tolerance_seconds": "300",
		"photonpay_base_url":                       "https://x-api.sandbox.photontech.cc",
		"photonpay_card_form_factor":               "virtual_card",
		"photonpay_card_scheme":                    "Discover",
		"photonpay_card_type":                      "recharge",
		"photonpay_primary_currency":               "USD",
		"photonpay_transaction_limit_type":         "unlimited",
		"photonpay_webhook_tolerance_seconds":      "300",
		"dogpay_base_url":                          "https://sandbox-api-v2.dogpay.com",
		"dogpay_card_type":                         "virtual",
		"dogpay_velocity_amount_limit":             "0",
		"kimoox_base_url":                          "https://card.kimoox.com",
		"kimoox_card_type":                         "PREPAID",
		"kimoox_webhook_tolerance_seconds":         "300",
		"kimoox_apply_poll_attempts":               "30",
		"kimoox_apply_poll_interval_seconds":       "2",
	}
	for key, value := range defaults {
		var existing models.AppConfig
		result := database.Where("key = ?", key).First(&existing)
		if result.Error == nil {
			if key == "card_pool_card_creation_mode" && strings.EqualFold(strings.TrimSpace(existing.Value), "POOL_ONLY") {
				if err := database.Model(&existing).Update("value", "CREATE_ON_DEMAND").Error; err != nil {
					return fmt.Errorf("upgrade card pool creation mode: %w", err)
				}
			}
			continue
		}
		if result.Error != gorm.ErrRecordNotFound {
			return result.Error
		}
		if err := database.Create(&models.AppConfig{Key: key, Value: value}).Error; err != nil {
			return fmt.Errorf("seed config %s: %w", key, err)
		}
	}
	return nil
}

func seedCardPools(database *gorm.DB) error {
	legacy := models.CardPool{
		ID: "pool_legacy", Name: "LEGACY", Type: "LOCAL", UsageType: "ONE_TIME",
		Currency: "USD", RoutingStrategy: "FIXED", DefaultProvider: "LOCAL_TEXT", CardCreationMode: "CREATE_ON_DEMAND", Enabled: true,
	}
	var existing models.CardPool
	result := database.Where("id = ?", legacy.ID).First(&existing)
	if result.Error == gorm.ErrRecordNotFound {
		if err := database.Create(&legacy).Error; err != nil {
			return fmt.Errorf("seed legacy card pool: %w", err)
		}
	} else if result.Error != nil {
		return result.Error
	} else if existing.UsageType == "MULTI_USE" && existing.CardCreationMode == "POOL_ONLY" {
		if err := database.Model(&existing).Updates(map[string]any{"usage_type": "ONE_TIME", "card_creation_mode": "CREATE_ON_DEMAND"}).Error; err != nil {
			return fmt.Errorf("upgrade legacy card pool defaults: %w", err)
		}
	}
	provider := models.CardPoolProvider{ID: "pool_legacy_local_text", PoolID: legacy.ID, Provider: "LOCAL_TEXT", Enabled: true, Priority: 1, Weight: 100}
	var existingProvider models.CardPoolProvider
	result = database.Where("pool_id = ? AND provider = ?", provider.PoolID, provider.Provider).First(&existingProvider)
	if result.Error == gorm.ErrRecordNotFound {
		if err := database.Create(&provider).Error; err != nil {
			return fmt.Errorf("seed legacy local text provider: %w", err)
		}
	} else if result.Error != nil {
		return result.Error
	}
	return database.Model(&models.CardAsset{}).Where("pool_id = '' OR pool_id IS NULL").Update("pool_id", legacy.ID).Error
}

func seedTaxFreeAddresses(database *gorm.DB) error {
	var count int64
	if err := database.Model(&models.TaxFreeAddress{}).Count(&count).Error; err != nil {
		return fmt.Errorf("count tax-free addresses: %w", err)
	}
	if count > 0 {
		return nil
	}

	seeds := []models.TaxFreeAddress{
		{ID: "address_ph_01", Region: "PH", Line1: "32nd Street corner 9th Avenue", City: "Taguig", State: "Metro Manila", PostalCode: "1634", Country: "PH", Active: true},
		{ID: "address_ph_02", Region: "PH", Line1: "One Bonifacio High Street", City: "Taguig", State: "Metro Manila", PostalCode: "1635", Country: "PH", Active: true},
		{ID: "address_ph_03", Region: "PH", Line1: "6787 Ayala Avenue", City: "Makati", State: "Metro Manila", PostalCode: "1226", Country: "PH", Active: true},
		{ID: "address_ph_04", Region: "PH", Line1: "Mactan Economic Zone II", City: "Lapu-Lapu", State: "Cebu", PostalCode: "6015", Country: "PH", Active: true},
		{ID: "address_ph_05", Region: "PH", Line1: "Cebu IT Park, Lahug", City: "Cebu City", State: "Cebu", PostalCode: "6000", Country: "PH", Active: true},
		{ID: "address_ph_06", Region: "PH", Line1: "Clark Freeport Zone, Bldg 2145", City: "Angeles", State: "Pampanga", PostalCode: "2009", Country: "PH", Active: true},
		{ID: "address_us_01", Region: "US", Line1: "1234 NW Flanders Street", City: "Portland", State: "OR", PostalCode: "97209", Country: "US", Active: true},
		{ID: "address_us_02", Region: "US", Line1: "567 Main Street", City: "Bozeman", State: "MT", PostalCode: "59715", Country: "US", Active: true},
		{ID: "address_us_03", Region: "US", Line1: "890 Market Street", City: "Wilmington", State: "DE", PostalCode: "19801", Country: "US", Active: true},
		{ID: "address_us_04", Region: "US", Line1: "45 Elm Street", City: "Concord", State: "NH", PostalCode: "03301", Country: "US", Active: true},
		{ID: "address_sg_01", Region: "SG", Line1: "1 Raffles Place #20-01", City: "Singapore", State: "Singapore", PostalCode: "048616", Country: "SG", Active: true},
		{ID: "address_sg_02", Region: "SG", Line1: "80 Robinson Road #02-00", City: "Singapore", State: "Singapore", PostalCode: "068898", Country: "SG", Active: true},
		{ID: "address_sg_03", Region: "SG", Line1: "1 Harbourfront Walk #01-153", City: "Singapore", State: "Singapore", PostalCode: "098585", Country: "SG", Active: true},
		{ID: "address_my_01", Region: "MY", Line1: "Level 15, Main Office Tower", City: "Labuan", State: "Labuan FT", PostalCode: "87000", Country: "MY", Active: true},
		{ID: "address_my_02", Region: "MY", Line1: "Lot 3A-1, Level 3A, Labuan Times Square", City: "Labuan", State: "Labuan FT", PostalCode: "87000", Country: "MY", Active: true},
		{ID: "address_my_03", Region: "MY", Line1: "Ground Floor, Wisma Oceanic", City: "Labuan", State: "Labuan FT", PostalCode: "87007", Country: "MY", Active: true},
	}
	if err := database.Create(&seeds).Error; err != nil {
		return fmt.Errorf("seed tax-free addresses: %w", err)
	}
	return nil
}

func NewID(prefix string) string {
	return prefix + "_" + uuid.NewString()
}
