//go:build !linux

package main

func openObserverJournal(string) (observerJournal, error) { return nil, errObserverJournalUnsupported }
