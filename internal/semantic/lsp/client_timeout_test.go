package lsp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLSP_Client_CallTimesOut verifies that a bounded callTimeout unblocks a
// Call against a server that received the request but never replies — the
// csharp-ls-stuck-in-MSBuild failure mode. Without the bound this Call would
// block forever and stall the enrichment WaitGroup.
func TestLSP_Client_CallTimesOut(t *testing.T) {
	c, serverIn, _, cleanup := newPipedClient(t)
	defer cleanup()

	c.SetCallTimeout(50 * time.Millisecond)

	// Fake server: consume the request so the client's framed send
	// completes, then go silent — never write a response.
	go func() {
		_, _ = readFramed(serverIn)
	}()

	done := make(chan error, 1)
	go func() { done <- c.Call("test/never", nil, nil) }()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timeout")
		assert.Contains(t, err.Error(), "test/never")
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not return after its call timeout — the timeout case never fired")
	}
}

// TestLSP_Client_CallHonorsTimeoutHappyPath verifies the timer case does not
// disturb the normal round-trip: a server that replies well within the bound
// still resolves successfully.
func TestLSP_Client_CallHonorsTimeoutHappyPath(t *testing.T) {
	c, serverIn, serverOut, cleanup := newPipedClient(t)
	defer cleanup()

	c.SetCallTimeout(5 * time.Second)

	go func() {
		body, ok := readFramed(serverIn)
		if !ok {
			return
		}
		var req jsonRPCRequest
		_ = json.Unmarshal(body, &req)
		writeFramed(t, serverOut, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`"ok"`),
		})
	}()

	var result string
	require.NoError(t, c.Call("test/echo", nil, &result))
	assert.Equal(t, "ok", result)
}

// TestLSP_Client_ReadLoopDropsOnMalformedFrames verifies the read loop does
// not spin forever on a server that emits a run of Content-Length-less header
// blocks: past the bounded tolerance it drops the connection (closing done so
// pending Call()s unblock) instead of burning a core on `continue`.
func TestLSP_Client_ReadLoopDropsOnMalformedFrames(t *testing.T) {
	c, _, serverOut, cleanup := newPipedClient(t)
	defer cleanup()

	// Each blank line is one header block that terminates immediately with
	// no Content-Length — a malformed frame. Emit comfortably more than the
	// drop threshold in one write; bufio buffers them, so readResponses
	// processes them without the pipe write blocking past the drop point.
	go func() {
		_, _ = serverOut.Write([]byte(strings.Repeat("\r\n", 128)))
	}()

	select {
	case <-c.Done():
		// readResponses returned and closed done — the spin guard fired.
	case <-time.After(2 * time.Second):
		t.Fatal("read loop did not drop the connection on a flood of malformed frames")
	}
}
