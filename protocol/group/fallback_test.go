package group

import (
	"context"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
)

type fallbackTestOutboundManager struct {
	outbounds map[string]adapter.Outbound
}

func newFallbackTestOutboundManager(outbounds ...adapter.Outbound) *fallbackTestOutboundManager {
	manager := &fallbackTestOutboundManager{
		outbounds: make(map[string]adapter.Outbound, len(outbounds)),
	}
	for _, outbound := range outbounds {
		manager.outbounds[outbound.Tag()] = outbound
	}
	return manager
}

func (m *fallbackTestOutboundManager) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	return nil
}

func (m *fallbackTestOutboundManager) Close() error {
	return nil
}

func (m *fallbackTestOutboundManager) Outbounds() []adapter.Outbound {
	outbounds := make([]adapter.Outbound, 0, len(m.outbounds))
	for _, outbound := range m.outbounds {
		outbounds = append(outbounds, outbound)
	}
	return outbounds
}

func (m *fallbackTestOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.outbounds[tag]
	return outbound, loaded
}

func (m *fallbackTestOutboundManager) Default() adapter.Outbound {
	return nil
}

func (m *fallbackTestOutboundManager) Remove(tag string) error {
	delete(m.outbounds, tag)
	return nil
}

func (m *fallbackTestOutboundManager) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any) error {
	return nil
}
