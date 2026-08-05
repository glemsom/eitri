// bench_test.go — read-path allocation benchmarks (issue #981).
//
// These benchmarks document the per-read allocation reduction from removing
// the deep-copy from the Session Manager read path: the default read path
// (Get / GetConversationShared) returns shared references with no per-read
// allocation for the conversation, while the explicit copy helpers
// (CopySession / CopyConversation) allocate proportional to conversation size.

package session_test

import (
	"fmt"
	"testing"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/session"
)

func benchManagerWithMessages(b *testing.B, n int) (*session.Manager, string) {
	mgr := session.NewManager(10, b.TempDir())
	sess, err := mgr.Create("browser-1")
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		mgr.AppendMessage(sess.ID, message.Message{
			Role:    role,
			Content: fmt.Sprintf("message %d with some content", i),
		})
	}
	return mgr, sess.ID
}

func benchReadPathFacade(b *testing.B, n int) {
	mgr, id := benchManagerWithMessages(b, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.Get(id)
	}
}

func benchCopySession(b *testing.B, n int) {
	mgr, id := benchManagerWithMessages(b, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.CopySession(id)
	}
}

func benchReadPathConversation(b *testing.B, n int) {
	mgr, id := benchManagerWithMessages(b, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.GetConversationShared(id)
	}
}

func benchCopyConversation(b *testing.B, n int) {
	mgr, id := benchManagerWithMessages(b, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.CopyConversation(id)
	}
}

func BenchmarkReadPathFacade_10Messages(b *testing.B)   { benchReadPathFacade(b, 10) }
func BenchmarkReadPathFacade_1000Messages(b *testing.B) { benchReadPathFacade(b, 1000) }
func BenchmarkCopySession_10Messages(b *testing.B)      { benchCopySession(b, 10) }
func BenchmarkCopySession_1000Messages(b *testing.B)    { benchCopySession(b, 1000) }

func BenchmarkReadPathConversation_1000Messages(b *testing.B) { benchReadPathConversation(b, 1000) }
func BenchmarkCopyConversation_1000Messages(b *testing.B)     { benchCopyConversation(b, 1000) }
