package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aravind/vault-spindle/internal/api"
	"github.com/aravind/vault-spindle/internal/models"
	"github.com/aravind/vault-spindle/internal/store"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://vault:vault@localhost:5432/vault?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skip integration tests: cannot connect to database: %v\n", err)
		os.Exit(0)
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "skip integration tests: database unreachable: %v\n", err)
		os.Exit(0)
	}

	sqlBytes, err := os.ReadFile("../migrations/001_init.sql")
	if err != nil {
		fmt.Fprintf(os.Stderr, "read migrations: %v\n", err)
		os.Exit(1)
	}
	if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func resetPlayer(t *testing.T, playerID string) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM ledger_entries WHERE player_id = $1`,
		`DELETE FROM idempotency_records WHERE player_id = $1`,
		`DELETE FROM inventory WHERE player_id = $1`,
		`DELETE FROM reward_claims WHERE player_id = $1`,
		`DELETE FROM wallets WHERE player_id = $1`,
	} {
		_, err := testPool.Exec(ctx, q, playerID)
		require.NoError(t, err)
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := store.New(testPool)
	srv := api.NewServer(st, nil)
	return httptest.NewServer(srv.Handler())
}

func postJSON(t *testing.T, url, idempotencyKey string, body any) (int, []byte) {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set(models.IdempotencyKeyHeader, idempotencyKey)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, data
}

func getWallet(t *testing.T, baseURL, playerID string) models.WalletView {
	t.Helper()
	resp, err := http.Get(baseURL + "/v1/wallets/" + playerID)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var w models.WalletView
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&w))
	return w
}

func TestDuplicateCreditAppliesOnce(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	player := "dup-credit-player"
	resetPlayer(t, player)

	body := map[string]any{"amount": 100, "reason": "battle-win"}
	key := "credit-key-1"

	status1, resp1 := postJSON(t, ts.URL+"/v1/wallets/"+player+"/credit", key, body)
	status2, resp2 := postJSON(t, ts.URL+"/v1/wallets/"+player+"/credit", key, body)

	assert.Equal(t, http.StatusOK, status1)
	assert.Equal(t, http.StatusOK, status2)
	assert.JSONEq(t, string(resp1), string(resp2))

	w := getWallet(t, ts.URL, player)
	assert.Equal(t, int64(100), w.Balance)
}

func TestConcurrentPurchasesOnlyOneSucceeds(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	player := "race-player"
	resetPlayer(t, player)

	_, _ = postJSON(t, ts.URL+"/v1/wallets/"+player+"/credit", "fund-race", map[string]any{
		"amount": 100, "reason": "seed",
	})

	const workers = 20
	var okCount atomic.Int32
	var failCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			status, _ := postJSON(t, ts.URL+"/v1/wallets/"+player+"/purchase", fmt.Sprintf("purchase-%d", i), map[string]any{
				"itemId": "sword",
				"price":  100,
			})
			switch status {
			case http.StatusOK:
				okCount.Add(1)
			case http.StatusConflict:
				failCount.Add(1)
			default:
				t.Errorf("unexpected status %d", status)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), okCount.Load(), "exactly one purchase should succeed")
	assert.Equal(t, int32(workers-1), failCount.Load())

	w := getWallet(t, ts.URL, player)
	assert.Equal(t, int64(0), w.Balance)
	assert.Len(t, w.Inventory, 1)
}

func TestDuplicatePurchaseSameResponse(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	player := "dup-purchase-player"
	resetPlayer(t, player)

	postJSON(t, ts.URL+"/v1/wallets/"+player+"/credit", "fund-dup", map[string]any{
		"amount": 500, "reason": "seed",
	})

	body := map[string]any{"itemId": "shield", "price": 200}
	key := "purchase-dup-key"

	s1, r1 := postJSON(t, ts.URL+"/v1/wallets/"+player+"/purchase", key, body)
	s2, r2 := postJSON(t, ts.URL+"/v1/wallets/"+player+"/purchase", key, body)

	assert.Equal(t, http.StatusOK, s1)
	assert.Equal(t, http.StatusOK, s2)
	assert.JSONEq(t, string(r1), string(r2))

	w := getWallet(t, ts.URL, player)
	assert.Equal(t, int64(300), w.Balance)
	assert.Equal(t, []string{"shield"}, w.Inventory)
}

func TestRewardClaimOnce(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	player := "reward-player"
	resetPlayer(t, player)

	body := map[string]string{"playerId": player}
	s1, r1 := postJSON(t, ts.URL+"/v1/rewards/daily-bonus/claim", "claim-key-1", body)
	s2, r2 := postJSON(t, ts.URL+"/v1/rewards/daily-bonus/claim", "claim-key-1", body)

	assert.Equal(t, http.StatusOK, s1)
	assert.Equal(t, http.StatusOK, s2)
	assert.JSONEq(t, string(r1), string(r2))

	var c1, c2 models.ClaimResponse
	require.NoError(t, json.Unmarshal(r1, &c1))
	require.NoError(t, json.Unmarshal(r2, &c2))
	assert.False(t, c1.AlreadyClaimed)
	assert.False(t, c2.AlreadyClaimed)

	w := getWallet(t, ts.URL, player)
	assert.Equal(t, []string{"daily-bonus"}, w.ClaimedRewards)
}

func TestRewardClaimWithoutKeyShowsAlreadyClaimed(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	player := "reward-no-key"
	resetPlayer(t, player)

	body := map[string]string{"playerId": player}
	s1, _ := postJSON(t, ts.URL+"/v1/rewards/weekly/claim", "", body)
	s2, r2 := postJSON(t, ts.URL+"/v1/rewards/weekly/claim", "", body)

	assert.Equal(t, http.StatusOK, s1)
	assert.Equal(t, http.StatusOK, s2)

	var c2 models.ClaimResponse
	require.NoError(t, json.Unmarshal(r2, &c2))
	assert.True(t, c2.AlreadyClaimed)
}

func TestCrashRollbackNoPartialPurchase(t *testing.T) {
	ctx := context.Background()
	st := store.New(testPool)
	player := "crash-player"
	resetPlayer(t, player)

	_, _, err := st.Credit(ctx, player, "crash-fund", "seed", 100)
	require.NoError(t, err)

	// Simulate kill -9 mid-transaction: debit without commit.
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `INSERT INTO idempotency_records (idempotency_key, operation, player_id, http_status) VALUES ('crash-purchase', 'purchase', $1, 0)`, player)
	require.NoError(t, err)

	var balance int64
	err = tx.QueryRow(ctx, `SELECT balance FROM wallets WHERE player_id = $1 FOR UPDATE`, player).Scan(&balance)
	require.NoError(t, err)
	require.Equal(t, int64(100), balance)

	_, err = tx.Exec(ctx, `UPDATE wallets SET balance = balance - 100 WHERE player_id = $1`, player)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO inventory (player_id, item_id) VALUES ($1, 'axe')`, player)
	require.NoError(t, err)

	// Process killed mid-transaction; rollback before commit.
	require.NoError(t, tx.Rollback(ctx))

	balance, inv, _, err := st.GetWallet(ctx, player)
	require.NoError(t, err)
	assert.Equal(t, int64(100), balance, "balance unchanged after aborted tx")
	assert.Empty(t, inv, "no item granted after aborted tx")

	// Retry after crash succeeds exactly once.
	result, hit, err := st.Purchase(ctx, player, "crash-purchase", "axe", 100)
	require.NoError(t, err)
	require.Nil(t, hit)
	require.Equal(t, int64(0), result.Balance)
	assert.Contains(t, result.Inventory, "axe")

	// Duplicate retry returns cached response, no double spend.
	_, hit2, err := st.Purchase(ctx, player, "crash-purchase", "axe", 100)
	require.NoError(t, err)
	require.NotNil(t, hit2)
	assert.Equal(t, 200, hit2.HTTPStatus)

	balance, inv, _, err = st.GetWallet(ctx, player)
	require.NoError(t, err)
	assert.Equal(t, int64(0), balance)
	assert.Len(t, inv, 1)
}

func TestInvalidInputRejected(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	player := "invalid-player"
	resetPlayer(t, player)

	cases := []struct {
		name string
		url  string
		key  string
		body string
	}{
		{"negative amount", ts.URL + "/v1/wallets/" + player + "/credit", "bad-1", `{"amount": -5, "reason": "x"}`},
		{"missing reason", ts.URL + "/v1/wallets/" + player + "/credit", "bad-2", `{"amount": 10}`},
		{"garbage json", ts.URL + "/v1/wallets/" + player + "/credit", "bad-3", `{not json}`},
		{"missing idempotency key", ts.URL + "/v1/wallets/" + player + "/credit", "", `{"amount": 10, "reason": "x"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, tc.url, bytes.NewBufferString(tc.body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			if tc.key != "" {
				req.Header.Set(models.IdempotencyKeyHeader, tc.key)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}

	w := getWallet(t, ts.URL, player)
	assert.Equal(t, int64(0), w.Balance)
}

func TestConcurrentDuplicateCredits(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	player := "concurrent-dup"
	resetPlayer(t, player)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)

	results := make([][]byte, n)
	statuses := make([]int, n)

	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			statuses[i], results[i] = postJSON(t, ts.URL+"/v1/wallets/"+player+"/credit", "same-key", map[string]any{
				"amount": 250, "reason": "battle",
			})
		}()
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		assert.Equal(t, http.StatusOK, statuses[i])
		assert.JSONEq(t, string(results[0]), string(results[i]))
	}

	w := getWallet(t, ts.URL, player)
	assert.Equal(t, int64(250), w.Balance)
}

func TestInsufficientFundsNoPartialEffect(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	player := "broke-player"
	resetPlayer(t, player)

	status, _ := postJSON(t, ts.URL+"/v1/wallets/"+player+"/purchase", "broke-buy", map[string]any{
		"itemId": "gem",
		"price":  50,
	})
	assert.Equal(t, http.StatusConflict, status)

	w := getWallet(t, ts.URL, player)
	assert.Equal(t, int64(0), w.Balance)
	assert.Empty(t, w.Inventory)

	// Cached rejection on retry.
	status2, _ := postJSON(t, ts.URL+"/v1/wallets/"+player+"/purchase", "broke-buy", map[string]any{
		"itemId": "gem",
		"price":  50,
	})
	assert.Equal(t, http.StatusConflict, status2)
}
