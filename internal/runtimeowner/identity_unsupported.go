//go:build !darwin && !linux

package runtimeowner

func observePlatformOwner(int) (Owner, error) { return Owner{}, errOwnerEvidence }
