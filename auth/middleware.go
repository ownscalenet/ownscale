package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/derrors"

	"github.com/markbates/goth/gothic"
)

// RequireAuthAPI loads the logged-in user (from the gothic session) into the
// request context and calls next. If no user is logged in, it responds with
// a JSON 401 instead of redirecting. Intended for /api/* routes consumed by
// the SPA via fetch().
func RequireAuthAPI(queries *sqlcgen.Queries, next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(queries, next, func(w http.ResponseWriter, r *http.Request) {
		writeUnauthenticated(w)
	})
}

func requireAuth(queries *sqlcgen.Queries, next http.HandlerFunc, onUnauthenticated http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := gothic.Store.Get(r, gothic.SessionName)
		if err != nil {
			onUnauthenticated(w, r)
			return
		}

		rawID, _ := sess.Values[SessionUserKey].(string)
		if rawID == "" {
			onUnauthenticated(w, r)
			return
		}

		user, err := queries.GetUserByID(r.Context(), rawID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			onUnauthenticated(w, r)
			return
		}

		next(w, r.WithContext(WithUser(r.Context(), &user)))
	}
}

func writeUnauthenticated(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthenticated"})
}

// StoreUserSession persists the user's id in the gothic session cookie.
//
// Note: gothic.CompleteUserAuth defers a call to gothic.Logout, which clears
// the *shared* per-request session (gorilla's Store.Get returns the same
// cached *Session for repeated calls within one request) and sets its
// Options.MaxAge to -1 (delete cookie). If AuthCallback calls this function
// after CompleteUserAuth, the session obtained here is that same mutated
// object, so we must restore a sane MaxAge or the browser will immediately
// discard the cookie we are trying to set.
func StoreUserSession(w http.ResponseWriter, r *http.Request, userID string) (err error) {
	defer derrors.Wrap(&err, "auth.StoreUserSession(w, r, %q)", userID)

	sess, err := gothic.Store.Get(r, gothic.SessionName)
	if err != nil {
		return err
	}
	sess.Values[SessionUserKey] = userID
	sess.Options.MaxAge = SessionMaxAge
	return sess.Save(r, w)
}

// ClearUserSession removes the user id from the gothic session cookie.
func ClearUserSession(w http.ResponseWriter, r *http.Request) (err error) {
	defer derrors.Wrap(&err, "auth.ClearUserSession(w, r)")

	sess, err := gothic.Store.Get(r, gothic.SessionName)
	if err != nil {
		return err
	}
	delete(sess.Values, SessionUserKey)
	sess.Options.MaxAge = -1
	return sess.Save(r, w)
}
