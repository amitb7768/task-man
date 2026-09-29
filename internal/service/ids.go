package service

import "taskman/internal/repo"

// NewID mints a new opaque primary-key id (repo.NewID); service code and
// its tests call it unqualified, as they did when they lived in repo.
func NewID() string { return repo.NewID() }
