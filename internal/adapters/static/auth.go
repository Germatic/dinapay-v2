package static

import (
	"context"
	"errors"
	"strings"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type Auth struct{ keys map[string]core.Principal }

func NewAuth(spec string) *Auth {
	a := &Auth{keys: map[string]core.Principal{}}
	for _, entry := range strings.Split(spec, ",") {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		ids := strings.SplitN(parts[1], ":", 2)
		p := core.Principal{AccountID: ids[0]}
		if len(ids) == 2 {
			p.MerchantID = ids[1]
		}
		a.keys[parts[0]] = p
	}
	return a
}

func (a *Auth) Authenticate(_ context.Context, token string) (core.Principal, error) {
	p, ok := a.keys[token]
	if !ok {
		return core.Principal{}, errors.New("invalid api key")
	}
	return p, nil
}
