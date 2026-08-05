package account

import "time"

// User 是平台登录账号。PasswordHash 只在服务端内部使用，永不序列化到 API。
type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	Name         string     `json:"name"`
	Email        string     `json:"email"`
	Phone        string     `json:"phone"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	Permissions  []string   `json:"permissions"`
	Enabled      bool       `json:"enabled"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

type Session struct {
	TokenHash string
	UserID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}
