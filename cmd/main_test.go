package main

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

var allStates = []State{Unpaid, Funded, Disputed, Released, Refunded, Cancelled}

type move struct{ from, to State }

// The rules, written out independently of the transitions table.
// Anything not listed here must be refused.
var allowedMoves = map[move]bool{
	{Unpaid, Funded}:     true,
	{Unpaid, Cancelled}:  true,
	{Funded, Released}:   true,
	{Funded, Disputed}:   true,
	{Funded, Refunded}:   true, // DECISION: direct refund allowed. Delete this line if refunds must go through a dispute.
	{Disputed, Released}: true,
	{Disputed, Refunded}: true,
}

// Checks all 36 pairs, including a state moving to itself.
func TestCanTransit_EveryPair(t *testing.T) {
	for _, from := range allStates {
		for _, to := range allStates {
			want := allowedMoves[move{from, to}]
			got := canTransit(from, to)
			if got != want {
				t.Errorf("canTransit(%q, %q) = %v, want %v", from, to, got, want)
			}
		}
	}
}

// Once money has reached an end state, nothing may move it again.
func TestCanTransit_TerminalStatesAreFinal(t *testing.T) {
	terminal := []State{Released, Refunded, Cancelled}
	for _, from := range terminal {
		for _, to := range allStates {
			if canTransit(from, to) {
				t.Errorf("terminal state %q must not move to %q", from, to)
			}
		}
	}
}

// An escrow must never skip the funding step.
func TestCanTransit_CannotSkipFunding(t *testing.T) {
	for _, to := range []State{Released, Refunded, Disputed} {
		if canTransit(Unpaid, to) {
			t.Errorf("unpaid must not move straight to %q", to)
		}
	}
}

// A state name we don't know about (a typo, bad data) must be refused.
func TestCanTransit_UnknownStateIsRefused(t *testing.T) {
	if canTransit(State("bogus"), Funded) {
		t.Error("unknown state should not be able to move anywhere")
	}
	if canTransit(Funded, State("bogus")) {
		t.Error("nothing should be able to move to an unknown state")
	}
}

// --- helpers ---

func validTrade() (Person, Person, Order) {
	seller := Person{Name: "Amina", Phone: "+256700000001"}
	buyer := Person{Name: "Joel", Phone: "+256700000002"}
	order := Order{
		Item:  "blue dress",
		Price: 50000,
		Terms: "one blue dress, size M, delivered by Friday",
	}
	return seller, buyer, order
}

// escrowIn builds a valid escrow and forces it into the given state,
// so a test can start from any state without walking there.
func escrowIn(t *testing.T, state State) *Escrow {
	t.Helper()
	s, b, o := validTrade()
	e, err := NewEscrow(s, b, o)
	if err != nil {
		t.Fatalf("setup: NewEscrow refused a valid trade: %v", err)
	}
	e.State = state
	return e
}

// --- creation ---

func TestNewEscrow_ValidTrade(t *testing.T) {
	s, b, o := validTrade()
	e, err := NewEscrow(s, b, o)
	if err != nil {
		t.Fatalf("NewEscrow(valid) returned error: %v", err)
	}
	if e == nil {
		t.Fatal("NewEscrow(valid) returned a nil escrow")
	}
	if e.ID == uuid.Nil {
		t.Error("new escrow has an empty ID")
	}
	if e.State != Unpaid {
		t.Errorf("new escrow state = %q, want %q", e.State, Unpaid)
	}
	if e.Seller != s || e.Buyer != b || e.Order != o {
		t.Errorf("escrow did not keep the input: got seller=%+v buyer=%+v order=%+v", e.Seller, e.Buyer, e.Order)
	}
}

func TestNewEscrow_BuyerNameMayBeEmpty(t *testing.T) {
	s, b, o := validTrade()
	b.Name = ""
	e, err := NewEscrow(s, b, o)
	if err != nil {
		t.Fatalf("a buyer with no name should be accepted, got error: %v", err)
	}
	if e == nil {
		t.Fatal("got a nil escrow for a valid trade with no buyer name")
	}
}

func TestNewEscrow_Refusals(t *testing.T) {
	cases := []struct {
		name   string
		change func(s, b *Person, o *Order)
	}{
		{"empty terms", func(s, b *Person, o *Order) { o.Terms = "" }},
		{"empty item", func(s, b *Person, o *Order) { o.Item = "" }},
		{"price zero", func(s, b *Person, o *Order) { o.Price = 0 }},
		{"price negative", func(s, b *Person, o *Order) { o.Price = -1 }},
		{"seller and buyer same phone", func(s, b *Person, o *Order) { b.Phone = s.Phone }},
		{"seller phone empty", func(s, b *Person, o *Order) { s.Phone = "" }},
		{"buyer phone empty", func(s, b *Person, o *Order) { b.Phone = "" }},
		{"seller name empty", func(s, b *Person, o *Order) { s.Name = "" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, b, o := validTrade()
			tc.change(&s, &b, &o)
			e, err := NewEscrow(s, b, o)
			if err == nil {
				t.Error("expected an error, got nil")
			}
			if e != nil {
				t.Errorf("expected no escrow to be returned, got %+v", e)
			}
		})
	}
}

func TestNewEscrow_IDsAreUnique(t *testing.T) {
	s, b, o := validTrade()
	first, err1 := NewEscrow(s, b, o)
	second, err2 := NewEscrow(s, b, o)
	if err1 != nil || err2 != nil {
		t.Fatalf("setup failed: %v, %v", err1, err2)
	}
	if first.ID == second.ID {
		t.Errorf("two escrows from the same input share the ID %v", first.ID)
	}
}

// --- moving between states ---

func TestMoveTo_LegalMovesSucceed(t *testing.T) {
	for m := range allowedMoves {
		t.Run(string(m.from)+" to "+string(m.to), func(t *testing.T) {
			e := escrowIn(t, m.from)
			if err := e.MoveTo(m.to); err != nil {
				t.Fatalf("MoveTo(%q) from %q returned error: %v", m.to, m.from, err)
			}
			if e.State != m.to {
				t.Errorf("state = %q, want %q", e.State, m.to)
			}
		})
	}
}

func TestMoveTo_IllegalMovesAreRefusedAndStateUnchanged(t *testing.T) {
	for _, from := range allStates {
		for _, to := range allStates {
			if from == to || allowedMoves[move{from, to}] {
				continue
			}
			t.Run(string(from)+" to "+string(to), func(t *testing.T) {
				e := escrowIn(t, from)
				err := e.MoveTo(to)
				if !errors.Is(err, ErrInvalidTransition) {
					t.Errorf("MoveTo(%q) from %q: error = %v, want ErrInvalidTransition", to, from, err)
				}
				if e.State != from {
					t.Errorf("state changed to %q after a refused move, want it to stay %q", e.State, from)
				}
			})
		}
	}
}

func TestMoveTo_SameStateIsItsOwnError(t *testing.T) {
	for _, st := range allStates {
		t.Run(string(st), func(t *testing.T) {
			e := escrowIn(t, st)
			err := e.MoveTo(st)
			if !errors.Is(err, ErrAlreadyInState) {
				t.Errorf("MoveTo(%q) while in %q: error = %v, want ErrAlreadyInState", st, st, err)
			}
			if errors.Is(err, ErrInvalidTransition) {
				t.Error("a repeat request must not look like an illegal move")
			}
			if e.State != st {
				t.Errorf("state changed to %q, want it to stay %q", e.State, st)
			}
		})
	}
}

func TestMoveTo_TerminalStatesStayTerminal(t *testing.T) {
	for _, from := range []State{Released, Refunded, Cancelled} {
		for _, to := range allStates {
			t.Run(string(from)+" to "+string(to), func(t *testing.T) {
				e := escrowIn(t, from)
				if err := e.MoveTo(to); err == nil {
					t.Error("expected an error, got nil")
				}
				if e.State != from {
					t.Errorf("terminal state changed to %q, want it to stay %q", e.State, from)
				}
			})
		}
	}
}

func TestMoveTo_ZeroValueEscrowIsRefused(t *testing.T) {
	e := &Escrow{}
	if err := e.MoveTo(Funded); err == nil {
		t.Error("an escrow with no state should not be able to move")
	}
	if e.State != "" {
		t.Errorf("state changed to %q, want it to stay empty", e.State)
	}
}

func TestMoveTo_HappyPath(t *testing.T) {
	s, b, o := validTrade()
	e, err := NewEscrow(s, b, o)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	for _, next := range []State{Funded, Released} {
		if err := e.MoveTo(next); err != nil {
			t.Fatalf("MoveTo(%q) returned error: %v", next, err)
		}
	}
	if e.State != Released {
		t.Errorf("final state = %q, want %q", e.State, Released)
	}
}

// The in-memory store must satisfy the Store interface.
var _ Store = (*MemoryStore)(nil)

// createValid makes a valid escrow and stores it under the given token.
func createValid(t *testing.T, s Store, token string) *Escrow {
	t.Helper()
	seller, buyer, order := validTrade()
	e, err := NewEscrow(seller, buyer, order)
	if err != nil {
		t.Fatalf("setup: NewEscrow refused a valid trade: %v", err)
	}
	got, err := s.Create(token, e)
	if err != nil {
		t.Fatalf("setup: Create returned error: %v", err)
	}
	return got
}

// --- Create and Get ---

func TestStore_CreateThenGet(t *testing.T) {
	s := NewMemoryStore()
	created := createValid(t, s, "tok-1")

	got, err := s.Get(created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != created.ID || got.State != Unpaid {
		t.Errorf("Get returned %+v, want the escrow that was created", got)
	}
}

func TestStore_GetUnknownIDIsNotFound(t *testing.T) {
	s := NewMemoryStore()
	got, err := s.Get(uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(unknown) error = %v, want ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("Get(unknown) returned %+v, want nil", got)
	}
}

func TestStore_GetReturnsACopy(t *testing.T) {
	s := NewMemoryStore()
	created := createValid(t, s, "tok-1")

	first, _ := s.Get(created.ID)
	first.State = Released // change the copy directly, without Update

	second, _ := s.Get(created.ID)
	if second.State != Unpaid {
		t.Errorf("stored state = %q after editing a copy, want %q", second.State, Unpaid)
	}
}

func TestStore_CreateStoresACopy(t *testing.T) {
	s := NewMemoryStore()
	seller, buyer, order := validTrade()
	e, _ := NewEscrow(seller, buyer, order)
	if _, err := s.Create("tok-1", e); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	e.State = Released // change the original after it was stored

	got, _ := s.Get(e.ID)
	if got.State != Unpaid {
		t.Errorf("stored state = %q after editing the original, want %q", got.State, Unpaid)
	}
}

// --- duplicate requests (tokens) ---

func TestStore_SameTokenSameDetailsReturnsFirst(t *testing.T) {
	s := NewMemoryStore()
	first := createValid(t, s, "tok-1")

	// an identical request arrives again with the same token
	seller, buyer, order := validTrade()
	again, _ := NewEscrow(seller, buyer, order) // has a different new ID
	got, err := s.Create("tok-1", again)
	if err != nil {
		t.Fatalf("repeat Create returned error: %v", err)
	}
	if got.ID != first.ID {
		t.Errorf("repeat Create returned ID %v, want the first escrow's ID %v", got.ID, first.ID)
	}
}

func TestStore_SameTokenDifferentDetailsIsAConflict(t *testing.T) {
	s := NewMemoryStore()
	first := createValid(t, s, "tok-1")

	seller, buyer, order := validTrade()
	order.Price = 5000 // same token, but the price changed
	changed, _ := NewEscrow(seller, buyer, order)
	got, err := s.Create("tok-1", changed)
	if !errors.Is(err, ErrTokenConflict) {
		t.Errorf("Create with changed details: error = %v, want ErrTokenConflict", err)
	}
	if got != nil {
		t.Errorf("Create with changed details returned %+v, want nil", got)
	}

	stored, _ := s.Get(first.ID)
	if stored.Order.Price != 50000 {
		t.Errorf("stored price = %d, want it unchanged at 50000", stored.Order.Price)
	}
}

func TestStore_DifferentTokensMakeSeparateEscrows(t *testing.T) {
	s := NewMemoryStore()
	a := createValid(t, s, "tok-1")
	b := createValid(t, s, "tok-2") // identical details, different token
	if a.ID == b.ID {
		t.Errorf("two different tokens produced the same escrow %v", a.ID)
	}
}

// --- Update ---

func TestStore_UpdateUnknownIDIsNotFound(t *testing.T) {
	s := NewMemoryStore()
	err := s.Update(uuid.New(), func(e *Escrow) error { return e.MoveTo(Funded) })
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestStore_UpdateAppliesTheChange(t *testing.T) {
	s := NewMemoryStore()
	created := createValid(t, s, "tok-1")

	err := s.Update(created.ID, func(e *Escrow) error { return e.MoveTo(Funded) })
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	got, _ := s.Get(created.ID)
	if got.State != Funded {
		t.Errorf("state = %q after Update, want %q", got.State, Funded)
	}
}

func TestStore_UpdateReturnsTheChangeError(t *testing.T) {
	s := NewMemoryStore()
	created := createValid(t, s, "tok-1")

	// unpaid -> released is illegal, so MoveTo refuses
	err := s.Update(created.ID, func(e *Escrow) error { return e.MoveTo(Released) })
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("Update error = %v, want ErrInvalidTransition", err)
	}
}

func TestStore_UpdateFailureSavesNothing(t *testing.T) {
	s := NewMemoryStore()
	created := createValid(t, s, "tok-1")

	boom := errors.New("something went wrong halfway")
	err := s.Update(created.ID, func(e *Escrow) error {
		e.State = Funded // change the copy, then fail
		return boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("Update error = %v, want the change function's error", err)
	}
	got, _ := s.Get(created.ID)
	if got.State != Unpaid {
		t.Errorf("state = %q after a failed Update, want %q", got.State, Unpaid)
	}
}

func TestStore_ConcurrentMovesOnlyOneWins(t *testing.T) {
	s := NewMemoryStore()
	created := createValid(t, s, "tok-1")

	const n = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, repeats := 0, 0

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Update(created.ID, func(e *Escrow) error { return e.MoveTo(Funded) })
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrAlreadyInState):
				repeats++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Errorf("%d updates succeeded, want exactly 1", wins)
	}
	if repeats != n-1 {
		t.Errorf("%d updates were refused as repeats, want %d", repeats, n-1)
	}
	got, _ := s.Get(created.ID)
	if got.State != Funded {
		t.Errorf("final state = %q, want %q", got.State, Funded)
	}
}

// --- fakes: a pretend provider and a pretend clock ---

var _ Provider = (*fakeProvider)(nil)

type requestCall struct {
	Ref    uuid.UUID
	Phone  string
	Amount int64
}

type fakeProvider struct {
	mu         sync.Mutex
	requests   []requestCall
	requestErr error
	checkErr   error
	results    map[uuid.UUID]PaymentResult
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{results: make(map[uuid.UUID]PaymentResult)}
}

func (f *fakeProvider) RequestPayment(ref uuid.UUID, phone string, amount int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.requestErr != nil {
		return f.requestErr
	}
	f.requests = append(f.requests, requestCall{ref, phone, amount})
	return nil
}

func (f *fakeProvider) CheckStatus(ref uuid.UUID) (PaymentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checkErr != nil {
		return PaymentResult{}, f.checkErr
	}
	if r, ok := f.results[ref]; ok {
		return r, nil
	}
	return PaymentResult{Status: PaymentPending}, nil // no answer yet
}

func (f *fakeProvider) setResult(ref uuid.UUID, r PaymentResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[ref] = r
}

func (f *fakeProvider) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeProvider) lastRequest() requestCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

type fakeClock struct{ now time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)}
}
func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// --- setup ---

type paymentEnv struct {
	svc    *PaymentService
	store  *MemoryStore
	prov   *fakeProvider
	clock  *fakeClock
	escrow *Escrow // an unpaid escrow, price 50000
}

func newPaymentEnv(t *testing.T) *paymentEnv {
	t.Helper()
	store := NewMemoryStore()
	prov := newFakeProvider()
	clock := newFakeClock()
	svc := NewPaymentService(store, prov, clock.Now)
	escrow := createValid(t, store, "tok-1")
	return &paymentEnv{svc, store, prov, clock, escrow}
}

func (env *paymentEnv) stateOf(t *testing.T) State {
	t.Helper()
	got, err := env.store.Get(env.escrow.ID)
	if err != nil {
		t.Fatalf("setup: Get returned error: %v", err)
	}
	return got.State
}

// force puts the stored escrow into any state, for test setup only.
func (env *paymentEnv) force(t *testing.T, st State) {
	t.Helper()
	err := env.store.Update(env.escrow.ID, func(e *Escrow) error {
		e.State = st
		return nil
	})
	if err != nil {
		t.Fatalf("setup: Update returned error: %v", err)
	}
}

func (env *paymentEnv) start(t *testing.T) *Payment {
	t.Helper()
	p, err := env.svc.StartPayment(env.escrow.ID)
	if err != nil {
		t.Fatalf("setup: StartPayment returned error: %v", err)
	}
	return p
}

func (env *paymentEnv) providerSays(ref uuid.UUID, status PaymentStatus, amount int64) {
	env.prov.setResult(ref, PaymentResult{Status: status, Amount: amount, TransactionID: "tx-123"})
}

// --- starting a payment ---

func TestStartPayment_CreatesPendingAttempt(t *testing.T) {
	env := newPaymentEnv(t)
	p, err := env.svc.StartPayment(env.escrow.ID)
	if err != nil {
		t.Fatalf("StartPayment returned error: %v", err)
	}
	if p.Reference == uuid.Nil {
		t.Error("payment has an empty reference")
	}
	if p.EscrowID != env.escrow.ID {
		t.Errorf("EscrowID = %v, want %v", p.EscrowID, env.escrow.ID)
	}
	if p.Amount != env.escrow.Order.Price {
		t.Errorf("Amount = %d, want the escrow price %d", p.Amount, env.escrow.Order.Price)
	}
	if p.Status != PaymentPending {
		t.Errorf("Status = %q, want %q", p.Status, PaymentPending)
	}
	if !p.CreatedAt.Equal(env.clock.Now()) {
		t.Errorf("CreatedAt = %v, want %v", p.CreatedAt, env.clock.Now())
	}

	if env.prov.requestCount() != 1 {
		t.Fatalf("provider got %d requests, want 1", env.prov.requestCount())
	}
	req := env.prov.lastRequest()
	if req.Ref != p.Reference || req.Phone != env.escrow.Buyer.Phone || req.Amount != env.escrow.Order.Price {
		t.Errorf("provider request = %+v, want the payment's reference, the buyer's phone and the price", req)
	}
}

func TestStartPayment_UnknownEscrow(t *testing.T) {
	env := newPaymentEnv(t)
	p, err := env.svc.StartPayment(uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
	if p != nil {
		t.Errorf("got payment %+v, want nil", p)
	}
	if env.prov.requestCount() != 0 {
		t.Errorf("provider got %d requests, want 0", env.prov.requestCount())
	}
}

func TestStartPayment_RefusedUnlessEscrowIsUnpaid(t *testing.T) {
	for _, st := range allStates {
		if st == Unpaid {
			continue
		}
		t.Run(string(st), func(t *testing.T) {
			env := newPaymentEnv(t)
			env.force(t, st)
			_, err := env.svc.StartPayment(env.escrow.ID)
			if !errors.Is(err, ErrNotPayable) {
				t.Errorf("error = %v, want ErrNotPayable", err)
			}
			if env.prov.requestCount() != 0 {
				t.Errorf("provider got %d requests, want 0", env.prov.requestCount())
			}
		})
	}
}

func TestStartPayment_SecondWhilePendingIsRefused(t *testing.T) {
	env := newPaymentEnv(t)
	env.start(t)

	p, err := env.svc.StartPayment(env.escrow.ID)
	if !errors.Is(err, ErrPaymentInProgress) {
		t.Errorf("error = %v, want ErrPaymentInProgress", err)
	}
	if p != nil {
		t.Errorf("got payment %+v, want nil", p)
	}
	if env.prov.requestCount() != 1 {
		t.Errorf("provider got %d requests, want 1", env.prov.requestCount())
	}
}

func TestStartPayment_ProviderRefusalLeavesNothingPending(t *testing.T) {
	env := newPaymentEnv(t)
	env.prov.requestErr = errors.New("provider is down")

	if _, err := env.svc.StartPayment(env.escrow.ID); err == nil {
		t.Fatal("expected an error when the provider refuses, got nil")
	}

	env.prov.requestErr = nil
	if _, err := env.svc.StartPayment(env.escrow.ID); err != nil {
		t.Errorf("a retry after a provider refusal returned error: %v", err)
	}
}

func TestStartPayment_NewAttemptAfterFailureGetsNewReference(t *testing.T) {
	env := newPaymentEnv(t)
	first := env.start(t)
	env.providerSays(first.Reference, PaymentFailed, 0)
	if _, err := env.svc.Check(first.Reference); err != nil {
		t.Fatalf("setup: Check returned error: %v", err)
	}

	second, err := env.svc.StartPayment(env.escrow.ID)
	if err != nil {
		t.Fatalf("a new attempt after a failed one returned error: %v", err)
	}
	if second.Reference == first.Reference {
		t.Error("the new attempt reused the old reference")
	}
}

func TestStartPayment_ConcurrentOnlyOneWins(t *testing.T) {
	env := newPaymentEnv(t)

	const n = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, inProgress := 0, 0

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := env.svc.StartPayment(env.escrow.ID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrPaymentInProgress):
				inProgress++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Errorf("%d attempts started, want exactly 1", wins)
	}
	if inProgress != n-1 {
		t.Errorf("%d attempts refused as in progress, want %d", inProgress, n-1)
	}
	if env.prov.requestCount() != 1 {
		t.Errorf("provider got %d requests, want 1", env.prov.requestCount())
	}
}

// --- checking a payment ---

func TestCheck_SuccessFundsTheEscrow(t *testing.T) {
	env := newPaymentEnv(t)
	p := env.start(t)
	env.providerSays(p.Reference, PaymentSucceeded, env.escrow.Order.Price)

	got, err := env.svc.Check(p.Reference)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if got.Status != PaymentSucceeded {
		t.Errorf("payment status = %q, want %q", got.Status, PaymentSucceeded)
	}
	if got.TransactionID != "tx-123" {
		t.Errorf("TransactionID = %q, want the provider's %q", got.TransactionID, "tx-123")
	}
	if st := env.stateOf(t); st != Funded {
		t.Errorf("escrow state = %q, want %q", st, Funded)
	}
}

func TestCheck_SuccessTwiceIsHarmless(t *testing.T) {
	env := newPaymentEnv(t)
	p := env.start(t)
	env.providerSays(p.Reference, PaymentSucceeded, env.escrow.Order.Price)

	for i := 1; i <= 2; i++ {
		if _, err := env.svc.Check(p.Reference); err != nil {
			t.Fatalf("Check #%d returned error: %v", i, err)
		}
	}
	if st := env.stateOf(t); st != Funded {
		t.Errorf("escrow state = %q, want %q", st, Funded)
	}
}

func TestCheck_AmountMismatchLeavesEscrowUnpaid(t *testing.T) {
	env := newPaymentEnv(t)
	p := env.start(t)
	env.providerSays(p.Reference, PaymentSucceeded, 1) // not the price

	_, err := env.svc.Check(p.Reference)
	if !errors.Is(err, ErrAmountMismatch) {
		t.Errorf("error = %v, want ErrAmountMismatch", err)
	}
	if st := env.stateOf(t); st != Unpaid {
		t.Errorf("escrow state = %q, want it to stay %q", st, Unpaid)
	}
}

func TestCheck_ProviderSaysFailed(t *testing.T) {
	env := newPaymentEnv(t)
	p := env.start(t)
	env.providerSays(p.Reference, PaymentFailed, 0)

	got, err := env.svc.Check(p.Reference)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if got.Status != PaymentFailed {
		t.Errorf("payment status = %q, want %q", got.Status, PaymentFailed)
	}
	if st := env.stateOf(t); st != Unpaid {
		t.Errorf("escrow state = %q, want it to stay %q", st, Unpaid)
	}
}

func TestCheck_StaysPendingBeforeFiveMinutes(t *testing.T) {
	env := newPaymentEnv(t)
	p := env.start(t)
	env.clock.Advance(4*time.Minute + 59*time.Second)

	got, err := env.svc.Check(p.Reference)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if got.Status != PaymentPending {
		t.Errorf("payment status = %q, want %q", got.Status, PaymentPending)
	}
}

func TestCheck_FailsAfterFiveMinutes(t *testing.T) {
	env := newPaymentEnv(t)
	p := env.start(t)
	env.clock.Advance(5*time.Minute + time.Second)

	got, err := env.svc.Check(p.Reference)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if got.Status != PaymentFailed {
		t.Errorf("payment status = %q, want %q", got.Status, PaymentFailed)
	}
	if st := env.stateOf(t); st != Unpaid {
		t.Errorf("escrow state = %q, want it to stay %q", st, Unpaid)
	}
	if _, err := env.svc.StartPayment(env.escrow.ID); err != nil {
		t.Errorf("a new attempt after a timeout returned error: %v", err)
	}
}

func TestCheck_ProviderErrorKeepsAttemptPending(t *testing.T) {
	env := newPaymentEnv(t)
	p := env.start(t)

	env.prov.checkErr = errors.New("provider unreachable")
	if _, err := env.svc.Check(p.Reference); err == nil {
		t.Fatal("expected an error when the provider is unreachable, got nil")
	}

	env.prov.checkErr = nil
	got, err := env.svc.Check(p.Reference)
	if err != nil {
		t.Fatalf("Check after recovery returned error: %v", err)
	}
	if got.Status != PaymentPending {
		t.Errorf("payment status = %q, want it still %q", got.Status, PaymentPending)
	}
}

func TestCheck_UnknownReference(t *testing.T) {
	env := newPaymentEnv(t)
	got, err := env.svc.Check(uuid.New())
	if !errors.Is(err, ErrPaymentNotFound) {
		t.Errorf("error = %v, want ErrPaymentNotFound", err)
	}
	if got != nil {
		t.Errorf("got payment %+v, want nil", got)
	}
}

// --- cancelling ---

func TestCancel_BlockedWhilePaymentPending(t *testing.T) {
	env := newPaymentEnv(t)
	env.start(t)

	err := env.svc.CancelEscrow(env.escrow.ID)
	if !errors.Is(err, ErrPaymentInProgress) {
		t.Errorf("error = %v, want ErrPaymentInProgress", err)
	}
	if st := env.stateOf(t); st != Unpaid {
		t.Errorf("escrow state = %q, want it to stay %q", st, Unpaid)
	}
}

func TestCancel_AllowedWithNoPayment(t *testing.T) {
	env := newPaymentEnv(t)
	if err := env.svc.CancelEscrow(env.escrow.ID); err != nil {
		t.Fatalf("CancelEscrow returned error: %v", err)
	}
	if st := env.stateOf(t); st != Cancelled {
		t.Errorf("escrow state = %q, want %q", st, Cancelled)
	}
}

func TestCancel_AllowedAfterAttemptTimedOut(t *testing.T) {
	env := newPaymentEnv(t)
	p := env.start(t)
	env.clock.Advance(6 * time.Minute)
	if _, err := env.svc.Check(p.Reference); err != nil {
		t.Fatalf("setup: Check returned error: %v", err)
	}

	if err := env.svc.CancelEscrow(env.escrow.ID); err != nil {
		t.Fatalf("CancelEscrow returned error: %v", err)
	}
	if st := env.stateOf(t); st != Cancelled {
		t.Errorf("escrow state = %q, want %q", st, Cancelled)
	}
}

func TestCancel_RefusedWhenNotUnpaid(t *testing.T) {
	env := newPaymentEnv(t)
	env.force(t, Funded)

	err := env.svc.CancelEscrow(env.escrow.ID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("error = %v, want ErrInvalidTransition", err)
	}
	if st := env.stateOf(t); st != Funded {
		t.Errorf("escrow state = %q, want it to stay %q", st, Funded)
	}
}