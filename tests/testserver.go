package tests

import (
	"testing"

	"github.com/foxcpp/maddy-storage/internal/usecase"
)

type Server struct {
	Accounts *usecase.Account
}

func InitTestServer(t *testing.T) *Server {
	// 1. Инициализировать репозитории.
	// 2. Инициализировать юзкейсы
	// 3. Инициализировать бэкенд IMAP (imap2.New)

}

func ConnectTestServer(t *testing.T, s *Server) {

}

type Conn struct {
	// Тип conn почти полностью можно скопировать из tests/conn.go основной репы.
}
