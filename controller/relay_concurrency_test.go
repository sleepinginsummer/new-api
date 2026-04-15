package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestGetChannelUsesContextConcurrencyWhenChannelMetaMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	common.SetContextKey(c, constant.ContextKeyChannelId, 123)
	common.SetContextKey(c, constant.ContextKeyChannelType, 1)
	common.SetContextKey(c, constant.ContextKeyChannelName, "test-channel")
	common.SetContextKey(c, constant.ContextKeyChannelConcurrency, int64(1))
	common.SetContextKey(c, constant.ContextKeyChannelAutoBan, true)

	channel, err := getChannel(c, &relaycommon.RelayInfo{}, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if channel == nil {
		t.Fatal("expected channel, got nil")
	}
	if channel.GetChannelConcurrency() != 1 {
		t.Fatalf("expected channel concurrency=1, got %d", channel.GetChannelConcurrency())
	}
}
