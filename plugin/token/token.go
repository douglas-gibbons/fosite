package authorize

import (
	"camlistore.org/pkg/context"
	. "github.com/ory-am/fosite"
	"net/http"
)

// CodeResponseTypeHandler is a response handler for the Authorize Code grant using the explicit grant type
// as defined in https://tools.ietf.org/html/rfc6749#section-4.1
type CodeResponseTypeHandler struct {
}

func (c *CodeResponseTypeHandler) HandleResponseType(_ context.Context, resp AuthorizeResponder, ar AuthorizeRequester, _ http.Request, session interface{}) error {
	// This let's us define multiple response types, for example open id connect's id_token
	if ar.GetResponseTypes().Has("token") {
		return nil
	}

	// Handler is not responsible for this request
	return ErrInvalidResponseType
}

func (c *CodeResponseTypeHandler) HandleGrantType() {

}
