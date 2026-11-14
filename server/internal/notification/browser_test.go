package notification

import (
	"sync"
	"testing"
)

func TestAudienceIsolationCoalescingAndAdminOnlyChanges(t *testing.T) {
	var broker Broker
	owner, closeOwner := broker.Subscribe("owner", false)
	defer closeOwner()
	other, closeOther := broker.Subscribe("other", false)
	defer closeOther()
	admin, closeAdmin := broker.Subscribe("admin", true)
	defer closeAdmin()
	broker.Publish(Change{Resource: "conversations", ID: "child", ParentConversationID: "parent"}, "owner")
	broker.Publish(Change{Resource: "conversations", ID: "child", ParentConversationID: "parent"}, "owner")
	broker.Publish(Change{Resource: "users", ID: "owner"})
	if changes := owner.Drain(); len(changes) != 1 || changes[0].ParentConversationID != "parent" {
		t.Fatal("owner scope or duplicate coalescing changed", changes)
	}
	if changes := other.Drain(); len(changes) != 0 {
		t.Fatal("changes leaked outside the audience", changes)
	}
	if changes := admin.Drain(); len(changes) != 2 {
		t.Fatal("admin missed resource or admin-only changes", changes)
	}
}

func TestConcurrentPublicationDrainAndUnsubscribe(t *testing.T) {
	var broker Broker
	var work sync.WaitGroup
	for range 8 {
		work.Go(func() {
			sub, stop := broker.Subscribe("owner", false)
			for range 64 {
				broker.Publish(Change{Resource: "agents", ID: "a"}, "owner")
				sub.Drain()
			}
			stop()
		})
	}
	work.Wait()
	if broker.HasSubscribers() {
		t.Fatal("unsubscribed listeners retained")
	}
}
