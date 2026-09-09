package webhooks

import (
	"testing"
	"time"
)

func TestGroupByWebhookPreservesDeliveryOrder(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	batch := []delivery{
		{id: "confirmed-a", webhookID: "a", createdAt: base.Add(time.Second)},
		{id: "created-b", webhookID: "b", createdAt: base},
		{id: "created-a", webhookID: "a", createdAt: base},
		{id: "confirmed-b", webhookID: "b", createdAt: base.Add(time.Second)},
	}
	groups := groupByWebhook(batch)
	if len(groups) != 2 {
		t.Fatalf("groups = %d", len(groups))
	}
	for _, group := range groups {
		if len(group) != 2 || group[0].id[:7] != "created" || group[1].id[:9] != "confirmed" {
			t.Fatalf("out of order group: %#v", group)
		}
	}
}
