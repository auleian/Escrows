/*
#Todo
1. seller creates account

#Trade
-Buyer contacts a seller for a product stating the exact terms they want
-Seller inits a transaction clearly stating the agreed terms
-Receive a message with the agreed terms and a link to confirm payment which triggers adding pin
-Buyer gets a four digit pin which will be shared with the seller to get funds on their mobile money/bank
-Seller gets message that payment has been made and can proceed with the order
-BUyer gets the product/service and shares pin for seller to receive money
-if issues, check agreed terms and if seller met them remit the money
-Incase buyer forgets or doesnt want to release the pin, release the funds
-Only dont release the funds if quality is undoubtedly band even if the agreed terms were met.


Requirements
-meta api
-momo/airtel money api

technologies
-golang
-postgres
*/

package main

import (
	"errors"
    "sync"
	"github.com/google/uuid"
)

var ErrInvalidTransition = errors.New("Escrow cannot move to that state from its current state")
var ErrAlreadyInState = errors.New("Escrow is already in that state")
var ErrNotFound = errors.New("escrow not found")
var ErrTokenConflict = errors.New("this request token was already used for a different trade")

type State string

// unpaid, funded, disputed, released, refunded, cancelled
const (
	Unpaid    State = "unpaid"
	Funded    State = "funded"
	Released  State = "released"
	Disputed  State = "disputed"
	Refunded  State = "refunded"
	Cancelled State = "cancelled"
)

type Person struct {
	Name  string
	Phone string
}

type Order struct {
	Item  string
	Price int64
	Terms string
}

type Escrow struct {
	ID     uuid.UUID
	Seller Person
	Buyer  Person
	Order  Order
	State  State
}

type Store interface {
	Create(token string, e *Escrow) (*Escrow, error)
	Get(id uuid.UUID) (*Escrow, error)
	Update(id uuid.UUID, change func(*Escrow) error) error
}

type MemoryStore struct {
	mu      sync.Mutex
	escrows map[uuid.UUID]Escrow
	tokens  map[string]uuid.UUID
}

var transitions = map[State][]State{
	Unpaid:    {Funded, Cancelled},
	Funded:    {Released, Refunded, Disputed},
	Disputed:  {Refunded, Released},
	Released:  {},
	Refunded:  {},
	Cancelled: {},
}

func canTransit(from, to State) bool {
	options := transitions[from]
	for _, c := range options {
		if c == to {
			return true
		}
	}
	return false
}

func NewEscrow(seller, buyer Person, order Order) (*Escrow, error) {
	if order.Item == "" {
		return nil, errors.New("item is required")
	}
	if order.Terms == "" {
		return nil, errors.New("terms are required")
	}
	if order.Price <= 0 {
		return nil, errors.New("price must be above zero")
	}
	if seller.Name == "" {
		return nil, errors.New("seller name is required")
	}
	if seller.Phone == "" {
		return nil, errors.New("seller phone is required")
	}
	if buyer.Phone == "" {
		return nil, errors.New("buyer phone is required")
	}
	if seller.Phone == buyer.Phone {
		return nil, errors.New("seller and buyer cannot be the same person")
	}

	return &Escrow{
		ID:     uuid.New(),
		Seller: seller,
		Buyer:  buyer,
		Order:  order,
		State:  Unpaid,
	}, nil
}

func (e *Escrow) MoveTo(to State) error {
	if e.State == to {
		return ErrAlreadyInState
	}
	if !canTransit(e.State, to) {
		return ErrInvalidTransition
	}
	e.State = to
	return nil
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		escrows: make(map[uuid.UUID]Escrow),
		tokens:  make(map[string]uuid.UUID),
	}
}

func (s *MemoryStore) Get(id uuid.UUID) (*Escrow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.escrows[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &e, nil
}

func (s *MemoryStore) Create(token string, e *Escrow) (*Escrow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id, seen := s.tokens[token]; seen {
		first := s.escrows[id]
		if first.Seller != e.Seller || first.Buyer != e.Buyer || first.Order != e.Order {
			return nil, ErrTokenConflict
		}
		return &first, nil
	}

	s.escrows[e.ID] = *e
	s.tokens[token] = e.ID
	stored := *e
	return &stored, nil
}

func (s *MemoryStore) Update(id uuid.UUID, change func(*Escrow) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.escrows[id]
	if !ok {
		return ErrNotFound
	}
	if err := change(&e); err != nil {
		return err
	}
	s.escrows[id] = e
	return nil
}

func main() {

}
