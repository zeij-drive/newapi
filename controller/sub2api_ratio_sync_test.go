package controller

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestSub2APIRatioSyncEncryptedOptionDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "ratio-sync.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}

			db, err := gorm.Open(driver, &gorm.Config{
				NamingStrategy: schema.NamingStrategy{TablePrefix: "ratio_sync_test_"},
			})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)

			oldDB, oldCryptoSecret, oldConfigSecret := model.DB, common.CryptoSecret, common.ConfigEncryptionSecret
			model.DB = db
			common.CryptoSecret = "ratio-sync-test-crypto-secret"
			common.ConfigEncryptionSecret = "ratio-sync-test-config-secret"
			t.Cleanup(func() {
				require.NoError(t, db.Migrator().DropTable(&model.Option{}))
				require.NoError(t, sqlDB.Close())
				model.DB, common.CryptoSecret, common.ConfigEncryptionSecret = oldDB, oldCryptoSecret, oldConfigSecret
			})

			require.NoError(t, db.AutoMigrate(&model.Option{}))
			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)

			state := initialSub2APIRatioSyncState()
			state.Secrets.AccessToken = "ratio-sync-access-token"
			state.Secrets.RefreshToken = "ratio-sync-refresh-token"
			require.NoError(t, saveSub2APIRatioSyncState(state))

			var stored model.Option
			require.NoError(t, db.Where(&model.Option{Key: sub2APIRatioSyncOptionKey}).First(&stored).Error)
			assert.True(t, strings.HasPrefix(stored.Value, "v2."))
			assert.NotContains(t, stored.Value, state.Secrets.AccessToken)
			assert.NotContains(t, stored.Value, state.Secrets.RefreshToken)

			loaded, err := loadSub2APIRatioSyncState()
			require.NoError(t, err)
			assert.Equal(t, state.Secrets.AccessToken, loaded.Secrets.AccessToken)
			assert.Equal(t, state.Secrets.RefreshToken, loaded.Secrets.RefreshToken)

			state.Settings.Interval = 120
			require.NoError(t, saveSub2APIRatioSyncState(state))
			var optionCount int64
			require.NoError(t, db.Model(&model.Option{}).Where(&model.Option{Key: sub2APIRatioSyncOptionKey}).Count(&optionCount).Error)
			assert.EqualValues(t, 1, optionCount)
			loaded, err = loadSub2APIRatioSyncState()
			require.NoError(t, err)
			assert.Equal(t, 120, loaded.Settings.Interval)
		})
	}
}

func TestNormalizeSub2APIBaseURLRequiresTLSOutsideLoopback(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "normalizes a secure remote URL", input: " HTTPS://sub2api.example/ ", want: "https://sub2api.example"},
		{name: "allows localhost for development", input: "http://localhost:3000", want: "http://localhost:3000"},
		{name: "allows IPv4 loopback for development", input: "http://127.0.0.1:3000", want: "http://127.0.0.1:3000"},
		{name: "allows IPv6 loopback for development", input: "http://[::1]:3000", want: "http://[::1]:3000"},
		{name: "rejects remote HTTP", input: "http://sub2api.example", wantErr: true},
		{name: "rejects private-network HTTP", input: "http://10.0.0.8", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeSub2APIBaseURL(test.input)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestSub2APIRatioSyncReceiverURLRequiresTLSOutsideLoopback(t *testing.T) {
	tests := []struct {
		name          string
		requestHost   string
		requestTLS    bool
		serverAddress string
		want          string
		wantErr       bool
	}{
		{name: "uses TLS from the request", requestHost: "newapi.example", requestTLS: true, want: "https://newapi.example/api/sub2api-ratio-sync/pair"},
		{name: "uses configured TLS behind a reverse proxy", requestHost: "internal:3000", serverAddress: "https://newapi.example/base", want: "https://newapi.example/api/sub2api-ratio-sync/pair"},
		{name: "allows loopback development", requestHost: "localhost:3000", want: "http://localhost:3000/api/sub2api-ratio-sync/pair"},
		{name: "rejects remote cleartext requests", requestHost: "newapi.example", wantErr: true},
		{name: "rejects an insecure configured address", requestHost: "newapi.example", requestTLS: true, serverAddress: "http://newapi.example", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := sub2APIRatioSyncReceiverURL(test.requestHost, test.requestTLS, test.serverAddress)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestSub2APIRatioSyncRetryDelayUsesCappedExponentialBackoff(t *testing.T) {
	tests := []struct {
		name     string
		interval int
		failures int
		want     int
	}{
		{name: "first failure uses the configured interval", interval: 60, failures: 1, want: 60},
		{name: "second failure doubles the interval", interval: 60, failures: 2, want: 120},
		{name: "retry delay is capped at thirty minutes", interval: 60, failures: 6, want: 1800},
		{name: "large intervals stay within the configured interval", interval: 3600, failures: 2, want: 3600},
		{name: "invalid interval uses the default", interval: 0, failures: 1, want: sub2APIRatioSyncInterval},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, sub2APIRatioSyncRetryDelay(test.interval, test.failures))
		})
	}
}

func TestValidateSub2APIRatioSyncMappings(t *testing.T) {
	tests := []struct {
		name     string
		mappings []sub2APIRatioSyncMapping
		wantErr  bool
	}{
		{
			name: "accepts unique enabled and disabled targets",
			mappings: []sub2APIRatioSyncMapping{
				{SourceID: 1, Target: "default", Mode: "effective", Value: 1, Enabled: true},
				{SourceID: 2, Target: "premium", Mode: "fixed", Value: 2.5, Enabled: false},
			},
		},
		{
			name: "rejects duplicate target groups",
			mappings: []sub2APIRatioSyncMapping{
				{SourceID: 1, Target: "default", Mode: "effective", Value: 1},
				{SourceID: 2, Target: "default", Mode: "fixed", Value: 2},
			},
			wantErr: true,
		},
		{
			name: "rejects non-finite values",
			mappings: []sub2APIRatioSyncMapping{
				{SourceID: 1, Target: "default", Mode: "fixed", Value: math.Inf(1)},
			},
			wantErr: true,
		},
		{
			name: "rejects missing group identity",
			mappings: []sub2APIRatioSyncMapping{
				{SourceID: 0, Target: "default", Mode: "effective", Value: 1},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSub2APIRatioSyncMappings(test.mappings)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestComputeSub2APIRatioSyncPlanAppliesModesAndSkipsMissingGroups(t *testing.T) {
	state := sub2APIRatioSyncState{Settings: sub2APIRatioSyncSettings{
		UpstreamURL:  "https://source.example",
		MappingScope: "https://source.example",
		Mappings: []sub2APIRatioSyncMapping{
			{SourceID: 1, Target: "effective-target", Mode: "effective", Value: 1.5, Enabled: true},
			{SourceID: 1, Target: "default-target", Mode: "default", Value: 2, Enabled: true},
			{SourceID: 1, Target: "fixed-target", Mode: "fixed", Value: 0.25, Enabled: true},
			{SourceID: 99, Target: "missing-target", Mode: "fixed", Value: 1, Enabled: true},
			{SourceID: 1, Target: "disabled-target", Mode: "fixed", Value: 4, Enabled: false},
		},
	}}
	groups := []sub2APIRatioSyncGroup{{
		ID: 1, Name: "Source", Default: 2, Effective: 3,
	}}
	current := map[string]float64{
		"effective-target": 1,
		"default-target":   2,
		"fixed-target":     0.5,
		"untouched":        7,
	}

	updated, rows, warnings, err := computeSub2APIRatioSyncPlan(state, groups, current)
	require.NoError(t, err)
	assert.Equal(t, 4.5, updated["effective-target"])
	assert.Equal(t, 4.0, updated["default-target"])
	assert.Equal(t, 0.25, updated["fixed-target"])
	assert.Equal(t, 7.0, updated["untouched"])
	assert.Len(t, rows, 3)
	assert.Len(t, warnings, 1)
}

func TestComputeSub2APIRatioSyncPlanRejectsUnapprovedPeakRates(t *testing.T) {
	state := sub2APIRatioSyncState{Settings: sub2APIRatioSyncSettings{
		UpstreamURL:  "https://source.example",
		MappingScope: "https://source.example",
		Mappings: []sub2APIRatioSyncMapping{{
			SourceID: 1, Target: "default", Mode: "effective", Value: 1, Enabled: true,
		}},
	}}
	groups := []sub2APIRatioSyncGroup{{
		ID: 1, Name: "Source", Default: 1, Effective: 1.2, Peak: true,
	}}

	_, _, _, err := computeSub2APIRatioSyncPlan(state, groups, map[string]float64{"default": 1})
	require.Error(t, err)
}

func TestComputeSub2APIRatioSyncPlanRejectsInvalidExistingRatios(t *testing.T) {
	state := sub2APIRatioSyncState{Settings: sub2APIRatioSyncSettings{
		UpstreamURL:  "https://source.example",
		MappingScope: "https://source.example",
		Mappings: []sub2APIRatioSyncMapping{{
			SourceID: 1, Target: "default", Mode: "fixed", Value: 1, Enabled: true,
		}},
	}}
	groups := []sub2APIRatioSyncGroup{{ID: 1, Name: "Source", Default: 1, Effective: 1}}

	for name, invalid := range map[string]float64{
		"negative": math.Copysign(1, -1),
		"infinite": math.Inf(1),
		"nan":      math.NaN(),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := computeSub2APIRatioSyncPlan(state, groups, map[string]float64{"default": invalid})
			require.Error(t, err)
		})
	}
}

func TestPublicSub2APIRatioSyncStateDoesNotExposeSessionTokens(t *testing.T) {
	state := initialSub2APIRatioSyncState()
	state.Secrets.AccessToken = "test-access-token"
	state.Secrets.RefreshToken = "test-refresh-token"

	encoded, err := common.Marshal(publicSub2APIRatioSyncState(state))
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), state.Secrets.AccessToken)
	assert.NotContains(t, string(encoded), state.Secrets.RefreshToken)
}
