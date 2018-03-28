package common

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/log"
	"github.com/status-im/status-go/static"
)

// All general log messages in this package should be routed through this logger.
var logger = log.New("package", "status-go/geth/common")

// ImportTestAccount imports keystore from static resources, see "static/keys" folder
func ImportTestAccount(keystoreDir, accountFile string) error {
	// make sure that keystore folder exists
	if _, err := os.Stat(keystoreDir); os.IsNotExist(err) {
		os.MkdirAll(keystoreDir, os.ModePerm) // nolint: errcheck, gas
	}

	dst := filepath.Join(keystoreDir, accountFile)
	err := ioutil.WriteFile(dst, static.MustAsset("keys/"+accountFile), 0644)
	if err != nil {
		logger.Warn("cannot copy test account PK", "error", err)
	}

	return err
}
