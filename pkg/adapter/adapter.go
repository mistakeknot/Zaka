// Package adapter exposes Zaka's CLI agent adapter API for orchestrators.
package adapter

import internal "github.com/mistakeknot/Zaka/internal/adapter"

type AgentAdapter = internal.AgentAdapter
type Config = internal.Config
type GenericAdapter = internal.GenericAdapter
type ClaudeAdapter = internal.ClaudeAdapter

func Register(a AgentAdapter) {
	internal.Register(a)
}

func Get(name string) AgentAdapter {
	return internal.Get(name)
}

func List() []string {
	return internal.List()
}

func NewGeneric(name, binary, cassConnector string, defaultArgs ...string) *GenericAdapter {
	return internal.NewGeneric(name, binary, cassConnector, defaultArgs...)
}

func FindLatestSession() (string, error) {
	return internal.FindLatestSession()
}
