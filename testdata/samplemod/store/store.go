// Package store has symbols whose names collide with other packages,
// letting tests verify that --pkg correctly scopes find results.
package store

// Store duplicates the name from alpha.Store — --pkg must disambiguate.
type Store struct{ ID int }

func (s *Store) Save() error   { return nil }
func (s *Store) Delete() error { return nil }
func (s *Store) Find() *Store  { return s }

// Top duplicates alpha.Top's name.
func Top(s *Store) *Store { return s.Find() }

const Version = 2

var DefaultStore = &Store{}
