package persistence

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/gobuffalo/pop/v6"
	"github.com/gofrs/uuid"
	"github.com/teamhanko/hanko/backend/persistence/models"
)

type UserPersister interface {
	Get(uuid.UUID) (*models.User, error)
	GetByEmailAddress(string) (*models.User, error)
	Create(models.User) error
	Update(models.User) error
	Delete(models.User) error
	List(page int, perPage int, userIDs []uuid.UUID, email string, username string, sortDirection string) ([]models.User, error)
	All() ([]models.User, error)
	Count(userIDs []uuid.UUID, email string, username string) (int, error)
	GetByUsername(username string) (*models.User, error)
}

type userPersister struct {
	db *pop.Connection
}

func NewUserPersister(db *pop.Connection) UserPersister {
	return &userPersister{db: db}
}

func (p *userPersister) Get(id uuid.UUID) (*models.User, error) {
	user := models.User{}

	eagerPreloadFields := []string{
		"Emails",
		"Emails.PrimaryEmail",
		"Emails.Identities.SamlIdentity",
		"WebauthnCredentials",
		"WebauthnCredentials.Transports",
		"Username",
		"PasswordCredential",
		"OTPSecret",
		"Metadata",
	}

	err := p.db.EagerPreload(eagerPreloadFields...).Find(&user, id)
	if err != nil && errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &user, nil
}

func (p *userPersister) GetByEmailAddress(emailAddress string) (*models.User, error) {
	email := models.Email{}
	err := p.db.Eager().Where("address = (?)", emailAddress).First(&email)

	if err != nil && errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get user by email address: %w", err)
	}

	if email.UserID == nil {
		return nil, nil
	}

	return p.Get(*email.UserID)
}

func (p *userPersister) GetByUsername(username string) (*models.User, error) {
	user := models.User{}
	err := p.db.EagerPreload(
		"Emails",
		"Emails.PrimaryEmail",
		"Emails.Identities",
		"WebauthnCredentials",
		"PasswordCredential",
		"Username",
		"OTPSecret",
		"Metadata").
		LeftJoin("usernames", "usernames.user_id = users.id").
		Where("usernames.username = (?)", username).
		First(&user)
	if err != nil && errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &user, nil
}

func (p *userPersister) Create(user models.User) error {
	vErr, err := p.db.ValidateAndCreate(&user)
	if err != nil {
		return fmt.Errorf("failed to store user: %w", err)
	}

	if vErr != nil && vErr.HasAny() {
		return fmt.Errorf("user object validation failed: %w", vErr)
	}

	return nil
}

func (p *userPersister) Update(user models.User) error {
	vErr, err := p.db.ValidateAndUpdate(&user)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	if vErr != nil && vErr.HasAny() {
		return fmt.Errorf("user object validation failed: %w", vErr)
	}

	return nil
}

func (p *userPersister) Delete(user models.User) error {
	err := p.db.Destroy(&user)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	return nil
}

func (p *userPersister) List(page int, perPage int, userIDs []uuid.UUID, email string, username string, sortDirection string) ([]models.User, error) {
	users := []models.User{}

	query := p.db.
		Q().
		EagerPreload(
			"Emails",
			"Emails.PrimaryEmail",
			"WebauthnCredentials",
			"WebauthnCredentials.Transports",
			"Username").
		LeftJoin("emails", "emails.user_id = users.id").
		LeftJoin("usernames", "usernames.user_id = users.id")
	query = p.addQueryParamsToSqlQuery(query, userIDs, email, username)
	err := query.GroupBy("users.id").
		Having("count(emails.id) > 0 OR count(usernames.id) > 0").
		Order(fmt.Sprintf("users.created_at %s", sortDirection)).
		Paginate(page, perPage).
		All(&users)

	if err != nil && errors.Is(err, sql.ErrNoRows) {
		return users, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fetch users: %w", err)
	}

	return users, nil
}

func (p *userPersister) All() ([]models.User, error) {
	users := []models.User{}

	err := p.db.EagerPreload(
		"Emails",
		"Emails.PrimaryEmail",
		"Emails.Identities",
		"WebauthnCredentials",
		"WebauthnCredentials.Transports",
		"Username",
	).All(&users)

	if err != nil && errors.Is(err, sql.ErrNoRows) {
		return users, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to fetch users: %w", err)
	}

	return users, nil
}

func (p *userPersister) Count(userIDs []uuid.UUID, email string, username string) (int, error) {
	query := p.db.
		Q().
		LeftJoin("emails", "emails.user_id = users.id").
		LeftJoin("usernames", "usernames.user_id = users.id")
	query = p.addQueryParamsToSqlQuery(query, userIDs, email, username)
	count, err := query.GroupBy("users.id").
		Having("count(emails.id) > 0 OR count(usernames.id) > 0").
		Count(&models.User{})
	if err != nil {
		return 0, fmt.Errorf("failed to get user count: %w", err)
	}

	return count, nil
}

func (p *userPersister) addQueryParamsToSqlQuery(query *pop.Query, userIDs []uuid.UUID, email string, username string) *pop.Query {
	if email != "" && username != "" {
		query = query.Where("emails.address LIKE ? OR usernames.username LIKE ?", "%"+email+"%", "%"+username+"%")
	} else if email != "" {
		query = query.Where("emails.address LIKE ?", "%"+email+"%")
	} else if username != "" {
		query = query.Where("usernames.username LIKE ?", "%"+username+"%")
	}

	if len(userIDs) > 0 {
		query = query.Where("users.id in (?)", userIDs)
	}

	return query
}
