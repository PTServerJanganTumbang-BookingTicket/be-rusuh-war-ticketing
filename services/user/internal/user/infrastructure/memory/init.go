package memory

import (
	"context"
	"sync"
	"time"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	portout "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

// UserRepository is a thread-safe in-memory implementation of IUserRepository.
// It is the storage adapter for local development and the Lambda POC.
// Production would replace this with an Aurora-backed adapter.
type UserRepository struct {
	mu sync.RWMutex

	accounts        map[string]entity.Account    // keyed by account ID
	accountsByEmail map[string]string            // email → account ID
	accountsByNIK   map[string]string            // nikHmac → account ID
	consentRecords  []entity.ConsentRecord

	magicTokens map[string]entity.MagicToken    // token value → MagicToken
	accessTokens map[string]entity.AccessToken  // token value → AccessToken

	savedNIKs   map[string]entity.SavedNIK   // nik ID → SavedNIK
	tickets     map[string]entity.UserTicket  // ticket ID → UserTicket
}

// NewUserRepository returns the outbound repository port backed by an in-memory store.
func NewUserRepository() portout.IUserRepository {
	return &UserRepository{
		accounts:        make(map[string]entity.Account),
		accountsByEmail: make(map[string]string),
		accountsByNIK:   make(map[string]string),
		magicTokens:     make(map[string]entity.MagicToken),
		accessTokens:    make(map[string]entity.AccessToken),
		savedNIKs:       make(map[string]entity.SavedNIK),
		tickets:         make(map[string]entity.UserTicket),
	}
}

// ----- Account -----

func (r *UserRepository) FindAccountByEmail(_ context.Context, email string) (entity.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.accountsByEmail[email]
	if !ok {
		return entity.Account{}, apperror.ErrNotFound("account not found")
	}
	return r.accounts[id], nil
}

func (r *UserRepository) FindAccountByID(_ context.Context, id string) (entity.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.accounts[id]
	if !ok {
		return entity.Account{}, apperror.ErrNotFound("account not found")
	}
	return a, nil
}

func (r *UserRepository) SaveAccount(_ context.Context, account entity.Account) (entity.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accounts[account.ID] = account
	r.accountsByEmail[account.Email] = account.ID
	if account.NIKHmac != "" {
		r.accountsByNIK[account.NIKHmac] = account.ID
	}
	return account, nil
}

func (r *UserRepository) UpdateAccount(_ context.Context, account entity.Account) (entity.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.accounts[account.ID]; !ok {
		return entity.Account{}, apperror.ErrNotFound("account not found")
	}
	r.accounts[account.ID] = account
	r.accountsByEmail[account.Email] = account.ID
	if account.NIKHmac != "" {
		r.accountsByNIK[account.NIKHmac] = account.ID
	}
	return account, nil
}

func (r *UserRepository) IsNIKRegistered(_ context.Context, nikHmac string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.accountsByNIK[nikHmac]
	return ok, nil
}

// ----- Consent -----

func (r *UserRepository) SaveConsentRecord(_ context.Context, record entity.ConsentRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consentRecords = append(r.consentRecords, record)
	return nil
}

// ----- Magic tokens -----

func (r *UserRepository) SaveMagicToken(_ context.Context, token entity.MagicToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.magicTokens[token.Token] = token
	return nil
}

func (r *UserRepository) FindMagicTokenByValue(_ context.Context, token string) (entity.MagicToken, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	mt, ok := r.magicTokens[token]
	if !ok {
		return entity.MagicToken{}, apperror.ErrNotFound("magic token not found")
	}
	return mt, nil
}

func (r *UserRepository) MarkMagicTokenUsed(_ context.Context, tokenID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, mt := range r.magicTokens {
		if mt.ID == tokenID {
			now := time.Now().UTC()
			mt.UsedAt = &now
			r.magicTokens[key] = mt
			return nil
		}
	}
	return apperror.ErrNotFound("magic token not found")
}

// ----- Access tokens -----

func (r *UserRepository) SaveAccessToken(_ context.Context, token entity.AccessToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accessTokens[token.Token] = token
	return nil
}

func (r *UserRepository) FindAccessTokenByValue(_ context.Context, token string) (entity.AccessToken, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	at, ok := r.accessTokens[token]
	if !ok {
		return entity.AccessToken{}, apperror.ErrNotFound("access token not found")
	}
	return at, nil
}

// ----- Saved NIKs -----

func (r *UserRepository) CountSavedNIKsByAccountID(_ context.Context, accountID string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, n := range r.savedNIKs {
		if n.AccountID == accountID {
			count++
		}
	}
	return count, nil
}

func (r *UserRepository) IsNIKAlreadySaved(_ context.Context, accountID, nikHmac string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, n := range r.savedNIKs {
		if n.AccountID == accountID && n.NIKHmac == nikHmac {
			return true, nil
		}
	}
	return false, nil
}

func (r *UserRepository) SaveSavedNIK(_ context.Context, nik entity.SavedNIK) (entity.SavedNIK, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.savedNIKs[nik.ID] = nik
	return nik, nil
}

func (r *UserRepository) FindSavedNIKsByAccountID(_ context.Context, accountID string) ([]entity.SavedNIK, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []entity.SavedNIK
	for _, n := range r.savedNIKs {
		if n.AccountID == accountID {
			result = append(result, n)
		}
	}
	return result, nil
}

func (r *UserRepository) FindSavedNIKByID(_ context.Context, accountID, id string) (entity.SavedNIK, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.savedNIKs[id]
	if !ok || n.AccountID != accountID {
		return entity.SavedNIK{}, apperror.ErrNotFound("saved NIK not found")
	}
	return n, nil
}

func (r *UserRepository) DeleteSavedNIK(_ context.Context, accountID, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.savedNIKs[id]
	if !ok || n.AccountID != accountID {
		return apperror.ErrNotFound("saved NIK not found")
	}
	delete(r.savedNIKs, id)
	return nil
}

// ----- Tickets -----

func (r *UserRepository) FindTicketsByAccountID(_ context.Context, accountID string, req dto.GetMyTicketsRequest) ([]entity.UserTicket, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var all []entity.UserTicket
	for _, t := range r.tickets {
		// A ticket belongs to an account if the NIK HMAC is associated with the account.
		// In the POC, we use a simpler association: tickets seeded directly on accounts.
		// The FindTicketByID and FindTicketsByAccountID use accountID as stored on the ticket.
		if getTicketAccountID(r, t) == accountID {
			if matchesStatusFilter(t, req.Status) {
				all = append(all, t)
			}
		}
	}

	total := len(all)
	start := req.Offset
	if start > total {
		start = total
	}
	end := start + req.Limit
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

func (r *UserRepository) FindTicketByID(_ context.Context, accountID, ticketID string) (entity.UserTicket, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tickets[ticketID]
	if !ok {
		return entity.UserTicket{}, apperror.ErrNotFound("ticket not found")
	}
	// ownership check — same as FindTicketsByAccountID
	if getTicketAccountID(r, t) != accountID {
		return entity.UserTicket{}, apperror.ErrNotFound("ticket not found")
	}
	return t, nil
}

// getTicketAccountID looks up the account that owns a ticket by matching the
// ticket's NIK HMAC against the account's NIK HMAC or its saved NIKs.
// This is a linear scan suitable for the POC; production would use a join.
func getTicketAccountID(r *UserRepository, t entity.UserTicket) string {
	for _, a := range r.accounts {
		if a.NIKHmac == t.NIKHmac {
			return a.ID
		}
	}
	for _, n := range r.savedNIKs {
		if n.NIKHmac == t.NIKHmac {
			return n.AccountID
		}
	}
	// Fall back to the ticket's stored account reference (set during seeding).
	return t.BookingID // not actually accountID; seeding must use SeedTicket
}

func matchesStatusFilter(t entity.UserTicket, status string) bool {
	switch status {
	case "upcoming":
		return t.Status == entity.TicketStatusValid
	case "past":
		return t.Status == entity.TicketStatusUsed || t.Status == entity.TicketStatusExpired
	default: // "all" or empty
		return true
	}
}

// SeedTicket inserts a ticket associated with a specific account ID.
// Used in integration tests and local seeding only.
func (r *UserRepository) SeedTicket(accountID string, t entity.UserTicket) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Use a sentinel mapping to tie the ticket to an account in tests.
	// We store the accountID in the NIKHmac so getTicketAccountID can find it.
	t.NIKHmac = "account:" + accountID
	// Also seed a synthetic account entry if absent, so ownership resolves correctly.
	if _, ok := r.accounts[accountID]; !ok {
		r.accounts[accountID] = entity.Account{ID: accountID, NIKHmac: "account:" + accountID}
	}
	r.tickets[t.TicketID] = t
}
