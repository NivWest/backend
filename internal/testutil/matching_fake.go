package testutil

import (
	"context"
	"sync"

	"backend/internal/domain"
)

var (
	_ domain.MatchingRepository = (*MatchingRepo)(nil)
	_ domain.LeaderLock         = (*LeaderLock)(nil)
)

// ---------------------------------------------------------------------------
// MatchingRepository (story NW-08)
// ---------------------------------------------------------------------------

// Rejection records one RejectOrder call that succeeded.
type Rejection struct {
	OrderID uint
	Reason  string
}

// MatchingRepo is an in-memory order book for the matching engine. Orders added with Add start OPEN.
//
// Defaults: ListOpen returns the OPEN orders by id. FillOrder and RejectOrder move an OPEN order to FILLED or
// REJECTED and record it (Fills, Rejections); an order that is no longer OPEN is ErrOrderNotOpen and an unknown
// one ErrOrderNotFound. Cancel does the same for CANCELLED, so wiring OrderRepo.CancelFn to it lets a cancel and
// the engine race on one book: every transition is a test-and-set under one mutex, like the row lock of the
// real repository, so exactly one of them wins. The Fn fields replace a default (FillOrderFn, e.g., to answer
// ErrInsufficientFunds); a FillOrderFn error leaves the status unchanged.
type MatchingRepo struct {
	mu         sync.Mutex
	orders     []domain.OpenOrder
	status     map[uint]domain.OrderStatus
	Fills      []domain.FillOrderParams
	Rejections []Rejection
	ListCalls  int

	ListOpenFn    func(ctx context.Context) ([]domain.OpenOrder, error)
	FillOrderFn   func(ctx context.Context, p domain.FillOrderParams) error
	RejectOrderFn func(ctx context.Context, orderID uint, reason string) error
}

// NewMatchingRepo returns a book holding the given OPEN orders.
func NewMatchingRepo(orders ...domain.OpenOrder) *MatchingRepo {
	r := &MatchingRepo{status: map[uint]domain.OrderStatus{}}
	for _, o := range orders {
		r.Add(o)
	}
	return r
}

// Add puts an OPEN order in the book.
func (r *MatchingRepo) Add(o domain.OpenOrder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == nil {
		r.status = map[uint]domain.OrderStatus{}
	}
	r.orders = append(r.orders, o)
	r.status[o.ID] = domain.OrderStatusOpen
}

// Status returns the order's status ("" when unknown).
func (r *MatchingRepo) Status(orderID uint) domain.OrderStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status[orderID]
}

func (r *MatchingRepo) ListOpen(ctx context.Context) ([]domain.OpenOrder, error) {
	r.mu.Lock()
	r.ListCalls++
	r.mu.Unlock()
	if r.ListOpenFn != nil {
		return r.ListOpenFn(ctx)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	open := []domain.OpenOrder{}
	for _, o := range r.orders {
		if r.status[o.ID] == domain.OrderStatusOpen {
			open = append(open, o)
		}
	}
	return open, nil
}

func (r *MatchingRepo) FillOrder(ctx context.Context, p domain.FillOrderParams) (*domain.Order, error) {
	if r.FillOrderFn != nil {
		if err := r.FillOrderFn(ctx, p); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.transitionLocked(p.OrderID, domain.OrderStatusFilled); err != nil {
		return nil, err
	}
	r.Fills = append(r.Fills, p)
	return &domain.Order{ID: p.OrderID, Status: domain.OrderStatusFilled, FillPrice: p.FillPrice, Commission: p.Commission, Total: p.Total}, nil
}

func (r *MatchingRepo) RejectOrder(ctx context.Context, orderID uint, reason string) (*domain.Order, error) {
	if r.RejectOrderFn != nil {
		if err := r.RejectOrderFn(ctx, orderID, reason); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.transitionLocked(orderID, domain.OrderStatusRejected); err != nil {
		return nil, err
	}
	r.Rejections = append(r.Rejections, Rejection{OrderID: orderID, Reason: reason})
	return &domain.Order{ID: orderID, Status: domain.OrderStatusRejected, RejectReason: &reason}, nil
}

// Cancel moves an OPEN order to CANCELLED, with the same rules as FillOrder. Its signature matches
// OrderRepo.CancelFn (the user id is ignored).
func (r *MatchingRepo) Cancel(_ context.Context, _ uint, orderID uint) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.transitionLocked(orderID, domain.OrderStatusCancelled); err != nil {
		return nil, err
	}
	return &domain.Order{ID: orderID, Status: domain.OrderStatusCancelled}, nil
}

func (r *MatchingRepo) transitionLocked(orderID uint, to domain.OrderStatus) error {
	st, ok := r.status[orderID]
	switch {
	case !ok:
		return domain.ErrOrderNotFound
	case st != domain.OrderStatusOpen:
		return domain.ErrOrderNotOpen
	}
	r.status[orderID] = to
	return nil
}

// ---------------------------------------------------------------------------
// LeaderLock (story NW-08)
// ---------------------------------------------------------------------------

// LeaderLock is a fake domain.LeaderLock. TryAcquire answers Held (or TryAcquireFn) and counts its calls.
type LeaderLock struct {
	mu       sync.Mutex
	Held     bool
	Attempts int
	Releases int

	TryAcquireFn func(ctx context.Context) (bool, error)
}

func (l *LeaderLock) TryAcquire(ctx context.Context) (bool, error) {
	l.mu.Lock()
	l.Attempts++
	fn, held := l.TryAcquireFn, l.Held
	l.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return held, nil
}

func (l *LeaderLock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Releases++
	l.Held = false
	return nil
}
