package identity

import (
	"context"
	"errors"
	"strings"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/srp6"
)

type Service struct {
	store    *Store
	ac       *account.Service
	maxLinks int
}

func New(store *Store, ac *account.Service, maxLinks int) *Service {
	if maxLinks < 1 {
		maxLinks = 5
	}
	return &Service{store: store, ac: ac, maxLinks: maxLinks}
}

func (s *Service) Store() *Store { return s.store }

func (s *Service) Authenticate(ctx context.Context, username, password string) (User, error) {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	u, err := s.store.GetByUsername(ctx, username)
	if errors.Is(err, ErrNotFound) {
		return s.claimLegacy(ctx, username, password)
	}
	if err != nil {
		return User{}, err
	}
	if NeedsLegacy(u.PasswordHash) {
		return s.claimLegacyExisting(ctx, u, password)
	}
	if !CheckPassword(u.PasswordHash, password) {
		return User{}, ErrBadPassword
	}
	if err := s.rejectIfBanned(ctx, u); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Service) UnlockDEK(ctx context.Context, userID uint32, password string) ([]byte, error) {
	u, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	kek, ok := VerifyAndKey(u.PasswordHash, password)
	if !ok {
		return nil, ErrBadPassword
	}
	return s.unwrapOrInit(ctx, userID, kek)
}

func (s *Service) unwrapOrInit(ctx context.Context, userID uint32, kek []byte) ([]byte, error) {
	wrap, nonce, err := s.store.DEKWrap(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(wrap) > 0 && len(nonce) > 0 {
		return open(kek, nonce, wrap)
	}
	dek, err := newDEK()
	if err != nil {
		return nil, err
	}
	n, b, err := seal(kek, dek)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetDEKWrap(ctx, userID, b, n); err != nil {
		return nil, err
	}
	return dek, nil
}

func (s *Service) SealClientPassword(ctx context.Context, userID uint32, wowUser, password string, dek []byte) error {
	if len(dek) != dekLen || password == "" {
		return errors.New("missing key")
	}
	nonce, blob, err := seal(dek, []byte(password))
	if err != nil {
		return err
	}
	return s.store.SetLinkSecret(ctx, userID, wowUser, blob, nonce)
}

func (s *Service) OpenClientPassword(l Link, dek []byte) (string, error) {
	if !l.HasSecret() {
		return "", nil
	}
	plain, err := open(dek, l.SecretNonce, l.SecretBlob)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Service) claimLegacy(ctx context.Context, username, password string) (User, error) {
	acc, err := s.ac.Authenticate(ctx, username, password)
	if err != nil {
		if errors.Is(err, account.ErrBanned) {
			return User{}, account.ErrBanned
		}
		if errors.Is(err, account.ErrBadPassword) {
			return User{}, ErrBadPassword
		}
		return User{}, err
	}
	u, err := s.EnsureFromAC(ctx, acc)
	if err != nil {
		return User{}, err
	}
	if err := s.setPassword(ctx, u.ID, password); err != nil {
		return User{}, err
	}
	if dek, err := s.UnlockDEK(ctx, u.ID, password); err == nil {
		_ = s.SealClientPassword(ctx, u.ID, acc.Username, password, dek)
	}
	u.PasswordHash = "" // not needed by caller
	return u, nil
}

func (s *Service) claimLegacyExisting(ctx context.Context, u User, password string) (User, error) {
	acc, err := s.ac.Authenticate(ctx, u.Username, password)
	if err != nil {
		if errors.Is(err, account.ErrBanned) {
			return User{}, account.ErrBanned
		}
		return User{}, ErrBadPassword
	}
	_ = s.store.Link(ctx, u.ID, acc.ID, acc.Username)
	if err := s.setPassword(ctx, u.ID, password); err != nil {
		return User{}, err
	}
	if dek, err := s.UnlockDEK(ctx, u.ID, password); err == nil {
		_ = s.SealClientPassword(ctx, u.ID, acc.Username, password, dek)
	}
	return u, nil
}

func (s *Service) setPassword(ctx context.Context, id uint32, password string) error {
	h, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.store.SetPassword(ctx, id, h)
}

func (s *Service) ChangePassword(ctx context.Context, user User, old, nw string) error {
	var dek []byte
	if NeedsLegacy(user.PasswordHash) {
		if _, err := s.ac.Authenticate(ctx, user.Username, old); err != nil {
			return ErrBadPassword
		}
	} else {
		kek, ok := VerifyAndKey(user.PasswordHash, old)
		if !ok {
			return ErrBadPassword
		}
		var err error
		dek, err = s.unwrapOrInit(ctx, user.ID, kek)
		if err != nil {
			return err
		}
	}
	kekNew, hash, err := HashPasswordAndKey(nw)
	if err != nil {
		return err
	}
	if err := s.store.SetPassword(ctx, user.ID, hash); err != nil {
		return err
	}
	if len(dek) == 0 {
		dek, err = newDEK()
		if err != nil {
			return err
		}
	}
	nonce, wrap, err := seal(kekNew, dek)
	if err != nil {
		return err
	}
	return s.store.SetDEKWrap(ctx, user.ID, wrap, nonce)
}

func (s *Service) SetPassword(ctx context.Context, username, password string) error {
	u, err := s.store.GetByUsername(ctx, username)
	if err != nil {
		return err
	}
	return s.setPassword(ctx, u.ID, password)
}

func (s *Service) EnsureFromAC(ctx context.Context, acc *account.Account) (User, error) {
	if acc == nil {
		return User{}, ErrNotFound
	}
	if ln, err := s.store.LinkByAccount(ctx, acc.ID); err == nil {
		return s.store.GetByID(ctx, ln.UserID)
	}
	if u, err := s.store.GetByUsername(ctx, acc.Username); err == nil {
		_ = s.store.Link(ctx, u.ID, acc.ID, acc.Username)
		return u, nil
	}
	u, err := s.store.CreateUser(ctx, acc.Username, acc.Email, migratedSentinel, acc.GMLevel)
	if err != nil {
		return User{}, err
	}
	if err := s.store.Link(ctx, u.ID, acc.ID, acc.Username); err != nil && !errors.Is(err, ErrLinkTaken) {
		return User{}, err
	}
	return u, nil
}

func (s *Service) Register(ctx context.Context, siteUser, sitePass, email, wowUser, wowPass string, expansion uint8) (User, error) {
	siteUser = srp6.UpperLatin(strings.TrimSpace(siteUser))
	wowUser = srp6.UpperLatin(strings.TrimSpace(wowUser))
	if wowUser == "" {
		wowUser = siteUser
	}
	taken, err := s.store.UsernameTaken(ctx, siteUser)
	if err != nil {
		return User{}, err
	}
	if taken {
		return User{}, ErrTaken
	}
	if email != "" {
		et, err := s.store.EmailTaken(ctx, email)
		if err != nil {
			return User{}, err
		}
		if et {
			return User{}, ErrEmailTaken
		}
	}
	acTaken, err := s.ac.UsernameTaken(ctx, wowUser)
	if err != nil {
		return User{}, err
	}
	if acTaken {
		return User{}, ErrTaken
	}
	kek, hash, err := HashPasswordAndKey(sitePass)
	if err != nil {
		return User{}, err
	}
	u, err := s.store.CreateUser(ctx, siteUser, email, hash, 0)
	if err != nil {
		return User{}, err
	}
	if err := s.ac.Create(ctx, wowUser, wowPass, email, expansion); err != nil {
		return User{}, err
	}
	listed, err := s.ac.GetListed(ctx, wowUser)
	if err != nil {
		return User{}, err
	}
	if err := s.store.Link(ctx, u.ID, listed.ID, wowUser); err != nil {
		return User{}, err
	}
	if dek, err := s.unwrapOrInit(ctx, u.ID, kek); err == nil {
		_ = s.SealClientPassword(ctx, u.ID, wowUser, wowPass, dek)
	}
	return u, nil
}

func (s *Service) AddCredential(ctx context.Context, userID uint32, wowUser, wowPass, email string, expansion uint8) (Link, error) {
	n, err := s.store.CountLinks(ctx, userID)
	if err != nil {
		return Link{}, err
	}
	if n >= s.maxLinks {
		return Link{}, ErrTooMany
	}
	wowUser = srp6.UpperLatin(strings.TrimSpace(wowUser))
	taken, err := s.ac.UsernameTaken(ctx, wowUser)
	if err != nil {
		return Link{}, err
	}
	if taken {
		return Link{}, ErrTaken
	}
	if err := s.ac.Create(ctx, wowUser, wowPass, email, expansion); err != nil {
		if errors.Is(err, account.ErrTaken) {
			return Link{}, ErrTaken
		}
		return Link{}, err
	}
	listed, err := s.ac.GetListed(ctx, wowUser)
	if err != nil {
		return Link{}, err
	}
	if err := s.store.Link(ctx, userID, listed.ID, wowUser); err != nil {
		return Link{}, err
	}
	return Link{UserID: userID, AccountID: listed.ID, Username: wowUser}, nil
}

func (s *Service) AddCredentialWithDEK(ctx context.Context, userID uint32, wowUser, wowPass, email string, expansion uint8, dek []byte) (Link, error) {
	ln, err := s.AddCredential(ctx, userID, wowUser, wowPass, email, expansion)
	if err != nil {
		return Link{}, err
	}
	if len(dek) == dekLen {
		if err := s.SealClientPassword(ctx, userID, wowUser, wowPass, dek); err != nil {
			return ln, err
		}
	}
	return ln, nil
}

func (s *Service) rejectIfBanned(ctx context.Context, u User) error {
	if s.ac == nil || u.ID == 0 {
		return nil
	}
	links, err := s.store.Links(ctx, u.ID)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(links)+1)
	for _, l := range links {
		names = append(names, l.Username)
	}
	if len(names) == 0 {
		names = append(names, u.Username)
	}
	for _, n := range names {
		if banned, _ := s.ac.IsBanned(ctx, n); banned {
			return account.ErrBanned
		}
	}
	return nil
}

func (s *Service) RejectIfBanned(ctx context.Context, u User) error {
	return s.rejectIfBanned(ctx, u)
}

func (s *Service) AccountIDs(ctx context.Context, userID uint32) ([]uint32, error) {
	return s.store.AccountIDs(ctx, userID)
}

func (s *Service) Links(ctx context.Context, userID uint32) ([]Link, error) {
	return s.store.Links(ctx, userID)
}

func (s *Service) GetByUsername(ctx context.Context, username string) (User, error) {
	return s.store.GetByUsername(ctx, username)
}

func (s *Service) GetByID(ctx context.Context, id uint32) (User, error) {
	return s.store.GetByID(ctx, id)
}

func (s *Service) GetByEmail(ctx context.Context, email string) (User, error) {
	return s.store.GetByEmail(ctx, email)
}

func (s *Service) UsernameTaken(ctx context.Context, username string) (bool, error) {
	return s.store.UsernameTaken(ctx, username)
}

func (s *Service) EmailTaken(ctx context.Context, email string) (bool, error) {
	return s.store.EmailTaken(ctx, email)
}

func (s *Service) OwnsAccount(ctx context.Context, userID, accountID uint32) bool {
	return s.store.OwnsAccount(ctx, userID, accountID)
}
