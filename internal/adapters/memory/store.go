package memory

import (
	"context"
	"sync"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type idempotencyRecord struct {
	Hash, TransactionID string
	Complete            bool
}

type Store struct {
	mu       sync.RWMutex
	payments map[string]core.Payment
	events   map[string]core.MerchantEvent
	keys     map[string]idempotencyRecord
}

func NewStore() *Store {
	return &Store{payments: map[string]core.Payment{}, events: map[string]core.MerchantEvent{}, keys: map[string]idempotencyRecord{}}
}

func (s *Store) BeginCreate(_ context.Context, merchantID, key, hash, transactionID string) (core.Payment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := merchantID + ":" + key
	if existing, ok := s.keys[scope]; ok {
		if existing.Hash != hash {
			return core.Payment{}, false, core.ErrConflict
		}
		if !existing.Complete {
			return core.Payment{}, false, core.ErrInProgress
		}
		return s.payments[existing.TransactionID], true, nil
	}
	s.keys[scope] = idempotencyRecord{Hash: hash, TransactionID: transactionID}
	return core.Payment{}, false, nil
}

func (s *Store) CompleteCreate(_ context.Context, p core.Payment, e core.MerchantEvent, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := p.MerchantID + ":" + key
	record, ok := s.keys[scope]
	if !ok || record.TransactionID != p.TransactionID {
		return core.ErrConflict
	}
	s.payments[p.TransactionID] = p
	s.events[e.EventID] = e
	record.Complete = true
	s.keys[scope] = record
	return nil
}

func (s *Store) ReleaseCreate(_ context.Context, merchantID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := merchantID + ":" + key
	if record, ok := s.keys[scope]; ok && !record.Complete {
		delete(s.keys, scope)
	}
	return nil
}

func (s *Store) Get(_ context.Context, accountID, merchantID, transactionID string) (core.Payment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.payments[transactionID]
	if !ok || (merchantID != "" && p.MerchantID != merchantID) || (merchantID == "" && (accountID == "" || p.AccountID != accountID)) {
		return core.Payment{}, core.ErrNotFound
	}
	return p, nil
}
