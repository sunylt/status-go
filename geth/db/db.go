package db

import (
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/errors"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

// CreateDatabase returns status wrapper to leveldb.
func CreateDatabase(path string) (*StatusDatabase, error) {
	opts := &opt.Options{OpenFilesCacheCapacity: 5}
	db, err := leveldb.OpenFile(path, opts)
	if _, iscorrupted := err.(*errors.ErrCorrupted); iscorrupted {
		db, err = leveldb.RecoverFile(path, nil)
	}
	if err != nil {
		return nil, err
	}
	return &StatusDatabase{db}, err
}

// StatusDatabase wrapper for leveldb.
type StatusDatabase struct {
	db *leveldb.DB
}

// PeersDatabase creates an instance of PeerDatabase wrapper
func (s *StatusDatabase) PeersDatabase() *PeersDatabase {
	return &PeersDatabase{s.db}
}
