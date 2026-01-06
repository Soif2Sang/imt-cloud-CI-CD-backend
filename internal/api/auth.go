package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/models"
)

var (
	jwtSecret = []byte(os.Getenv("JWT_SECRET"))

	googleOauthConfig *oauth2.Config
	githubOauthConfig *oauth2.Config
)

// InitializeOAuth configures the OAuth providers
func InitializeOAuth() {
	if len(jwtSecret) == 0 {
		jwtSecret = []byte("your-secret-key-should-be-in-env")
		log.Println("WARNING: JWT_SECRET not set, using default insecure key")
	}

	googleOauthConfig = &oauth2.Config{
		RedirectURL:  os.Getenv("API_URL") + "/auth/google/callback",
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		Scopes:       []string{"https://www.googleapis.com/auth/userinfo.email", "https://www.googleapis.com/auth/userinfo.profile"},
		Endpoint:     google.Endpoint,
	}

	githubOauthConfig = &oauth2.Config{
		RedirectURL:  os.Getenv("API_URL") + "/auth/github/callback",
		ClientID:     os.Getenv("GITHUB_CLIENT_ID"),
		ClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
		Scopes:       []string{"user:email", "read:user"},
		Endpoint:     github.Endpoint,
	}
}

// UserClaims represents the JWT claims
type UserClaims struct {
	UserID    int    `json:"user_id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
	jwt.RegisteredClaims
}

// handleAuthLogin redirects to the OAuth provider
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	// Extract provider from path /auth/{provider}/login
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	provider := pathParts[2] // auth, provider, login

	var config *oauth2.Config
	switch provider {
	case "google":
		config = googleOauthConfig
	case "github":
		config = githubOauthConfig
	default:
		http.Error(w, "Unsupported provider", http.StatusBadRequest)
		return
	}

	// Generate random state
	b := make([]byte, 16)
	rand.Read(b)
	state := base64.URLEncoding.EncodeToString(b)

	// Set state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "oauthstate",
		Value:    state,
		Expires:  time.Now().Add(10 * time.Minute),
		HttpOnly: true,
		Path:     "/",
	})

	url := config.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// handleAuthCallback handles the OAuth callback
func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	// Extract provider from path /auth/{provider}/callback
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	provider := pathParts[2]

	// Verify state
	oauthState, err := r.Cookie("oauthstate")
	if err != nil {
		http.Error(w, "State cookie not found", http.StatusBadRequest)
		return
	}
	if r.FormValue("state") != oauthState.Value {
		http.Error(w, "Invalid oauth state", http.StatusBadRequest)
		return
	}

	code := r.FormValue("code")
	var config *oauth2.Config

	switch provider {
	case "google":
		config = googleOauthConfig
	case "github":
		config = githubOauthConfig
	default:
		http.Error(w, "Unsupported provider", http.StatusBadRequest)
		return
	}

	token, err := config.Exchange(context.Background(), code)
	if err != nil {
		http.Error(w, "Code exchange failed", http.StatusInternalServerError)
		return
	}

	userInfo, err := getUserInfo(provider, token.AccessToken)
	if err != nil {
		log.Printf("Failed to get user info: %v", err)
		http.Error(w, "Failed to get user info", http.StatusInternalServerError)
		return
	}

	// Save/Update user in DB
	err = s.db.CreateUser(userInfo)
	if err != nil {
		log.Printf("Failed to save user: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Retrieve full user (with ID)
	dbUser, err := s.db.GetUserByEmail(userInfo.Email)
	if err != nil {
		log.Printf("Failed to retrieve user: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Create JWT
	jwtToken, err := createToken(dbUser)
	if err != nil {
		http.Error(w, "Failed to create token", http.StatusInternalServerError)
		return
	}

	// Redirect to frontend with token
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:3000"
	}
	http.Redirect(w, r, fmt.Sprintf("%s/auth/callback?token=%s", frontendURL, jwtToken), http.StatusTemporaryRedirect)
}

func getUserInfo(provider, accessToken string) (*models.User, error) {
	var req *http.Request
	var err error

	if provider == "google" {
		req, err = http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	} else if provider == "github" {
		req, err = http.NewRequest("GET", "https://api.github.com/user", nil)
	}

	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	user := &models.User{
		Provider: provider,
	}

	if provider == "google" {
		var googleUser struct {
			ID      string `json:"id"`
			Email   string `json:"email"`
			Name    string `json:"name"`
			Picture string `json:"picture"`
		}
		if err := json.Unmarshal(body, &googleUser); err != nil {
			return nil, err
		}
		user.ProviderID = googleUser.ID
		user.Email = googleUser.Email
		user.Name = googleUser.Name
		user.AvatarURL = googleUser.Picture
	} else if provider == "github" {
		var githubUser struct {
			ID        int    `json:"id"`
			Login     string `json:"login"`
			Email     string `json:"email"`
			Name      string `json:"name"`
			AvatarURL string `json:"avatar_url"`
		}
		if err := json.Unmarshal(body, &githubUser); err != nil {
			return nil, err
		}
		user.ProviderID = fmt.Sprintf("%d", githubUser.ID)
		user.Email = githubUser.Email
		if user.Email == "" {
			// Fetch emails if private - Simplified fallback
			user.Email = fmt.Sprintf("%s@github.com", githubUser.Login)
		}
		user.Name = githubUser.Name
		if user.Name == "" {
			user.Name = githubUser.Login
		}
		user.AvatarURL = githubUser.AvatarURL
	}

	return user, nil
}

func createToken(user *models.User) (string, error) {
	claims := UserClaims{
		UserID:    user.ID,
		Email:     user.Email,
		Name:      user.Name,
		AvatarURL: user.AvatarURL,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "imt-cloud-cicd",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// AuthMiddleware validates the JWT token
func (s *Server) AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "Invalid authorization header format", http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]
		claims := &UserClaims{}

		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		// Add user ID to context
		ctx := context.WithValue(r.Context(), "userID", claims.UserID)
		next(w, r.WithContext(ctx))
	}
}

// getUserIDFromContext helper to retrieve user ID
func getUserIDFromContext(r *http.Request) (int, error) {
	userID, ok := r.Context().Value("userID").(int)
	if !ok {
		return 0, fmt.Errorf("user ID not found in context")
	}
	return userID, nil
}