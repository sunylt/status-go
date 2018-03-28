package common

import (
	"encoding/json"
	"os"
	"time"

	"github.com/status-im/status-go/geth/params"
	"github.com/status-im/status-go/static"
)

type account struct {
	Address  string
	Password string
}

// TestConfig contains shared (among different test packages) parameters
type TestConfig struct {
	Node struct {
		SyncSeconds time.Duration
		HTTPPort    int
		WSPort      int
	}
	Account1 account
	Account2 account
	Account3 account
}

const passphraseEnvName = "ACCOUNT_PASSWORD"

// LoadTestConfig loads test configuration values from disk
func LoadTestConfig(networkID int) (*TestConfig, error) {
	var testConfig TestConfig

	configData := static.MustAsset("config/test-data.json")
	if err := json.Unmarshal(configData, &testConfig); err != nil {
		return nil, err
	}

	if networkID == params.StatusChainNetworkID {
		accountsData := static.MustAsset("config/status-chain-accounts.json")
		if err := json.Unmarshal(accountsData, &testConfig); err != nil {
			return nil, err
		}
	} else {
		accountsData := static.MustAsset("config/public-chain-accounts.json")
		if err := json.Unmarshal(accountsData, &testConfig); err != nil {
			return nil, err
		}

		pass := os.Getenv(passphraseEnvName)
		testConfig.Account1.Password = pass
		testConfig.Account2.Password = pass
	}

	return &testConfig, nil
}
