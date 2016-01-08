package fosite

import (
	"github.com/asaskevich/govalidator"
	"github.com/go-errors/errors"
	"github.com/ory-am/common/pkg"
	. "github.com/ory-am/fosite/client"
	"golang.org/x/net/context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const minStateLength = 8

func (c *Fosite) NewAuthorizeRequest(_ context.Context, r *http.Request) (AuthorizeRequester, error) {
	request := &AuthorizeRequest{
		RequestedAt: time.Now(),
	}

	if err := r.ParseForm(); err != nil {
		return request, errors.New(ErrInvalidRequest)
	}

	client, err := c.Store.GetClient(r.Form.Get("client_id"))
	if err != nil {
		return request, errors.New(ErrInvalidClient)
	}
	request.Client = client

	// Fetch redirect URI from request
	rawRedirURI, err := GetRedirectURIFromRequestValues(r.Form)
	if err != nil {
		return request, errors.New(ErrInvalidRequest)
	}

	// Validate redirect uri
	redirectURI, err := MatchRedirectURIWithClientRedirectURIs(rawRedirURI, client)
	if err != nil {
		return request, errors.New(ErrInvalidRequest)
	} else if !IsValidRedirectURI(redirectURI) {
		return request, errors.New(ErrInvalidRequest)
	}
	request.RedirectURI = redirectURI

	responseTypes := removeEmpty(strings.Split(r.Form.Get("response_type"), " "))
	request.ResponseTypes = responseTypes

	// rfc6819 4.4.1.8.  Threat: CSRF Attack against redirect-uri
	// The "state" parameter should be used to link the authorization
	// request with the redirect URI used to deliver the access token (Section 5.3.5).
	//
	// https://tools.ietf.org/html/rfc6819#section-4.4.1.8
	// The "state" parameter should not	be guessable
	state := r.Form.Get("state")
	if state == "" {
		return request, errors.New(ErrInvalidState)
	} else if len(state) < minStateLength {
		// We're assuming that using less then 6 characters for the state can not be considered "unguessable"
		return request, errors.New(ErrInvalidState)
	}
	request.State = state

	// Remove empty items from arrays
	request.Scopes = removeEmpty(strings.Split(r.Form.Get("scope"), " "))

	return request, nil
}

func (c *Fosite) WriteAuthorizeResponse(rw http.ResponseWriter, ar AuthorizeRequester, resp AuthorizeResponder) {
	redir := ar.GetRedirectURI()
	q := redir.Query()
	args := resp.GetQuery()
	for k, _ := range args {
		q.Add(k, args.Get(k))
	}
	redir.RawQuery = q.Encode()
	header := resp.GetHeader()
	for k, v := range header {
		for _, vv := range v {
			rw.Header().Add(k, vv)
		}
	}

	// https://tools.ietf.org/html/rfc6749#section-4.1.1
	// When a decision is established, the authorization server directs the
	// user-agent to the provided client redirection URI using an HTTP
	// redirection response, or by other means available to it via the
	// user-agent.
	rw.Header().Set("Location", ar.GetRedirectURI().String())
	rw.WriteHeader(http.StatusFound)
}

func (c *Fosite) WriteAuthorizeError(rw http.ResponseWriter, ar AuthorizeRequester, err error) {
	rfcerr := ErrorToRFC6749Error(err)

	if !ar.IsRedirectURIValid() {
		pkg.WriteJSON(rw, rfcerr)
		return
	}

	redirectURI := ar.GetRedirectURI()
	query := redirectURI.Query()
	query.Add("error", rfcerr.Name)
	query.Add("error_description", rfcerr.Description)
	redirectURI.RawQuery = query.Encode()

	rw.Header().Add("Location", redirectURI.String())
	rw.WriteHeader(http.StatusFound)
}

func (o *Fosite) NewAuthorizeResponse(ctx context.Context, ar AuthorizeRequester, r *http.Request, session interface{}) (AuthorizeResponder, error) {
	var resp = new(AuthorizeResponse)
	var err error
	var found bool

	for _, h := range o.ResponseTypeHandlers {
		err = h.HandleResponseType(ctx, resp, ar, r, session)
		if err == nil {
			found = true
		} else if err != ErrInvalidResponseType {
			return nil, err
		}
	}

	if !found {
		return nil, ErrNoResponseTypeHandlerFound
	}

	return resp, nil
}

// GetRedirectURIFromRequestValues extracts the redirect_uri from values but does not do any sort of validation.
//
// Considered specifications
// * https://tools.ietf.org/html/rfc6749#section-3.1
//   The endpoint URI MAY include an
//   "application/x-www-form-urlencoded" formatted (per Appendix B) query
//   component ([RFC3986] Section 3.4), which MUST be retained when adding
//   additional query parameters.
func GetRedirectURIFromRequestValues(values url.Values) (string, error) {
	// rfc6749 3.1.   Authorization Endpoint
	// The endpoint URI MAY include an "application/x-www-form-urlencoded" formatted (per Appendix B) query component
	redirectURI, err := url.QueryUnescape(values.Get("redirect_uri"))
	if err != nil {
		return "", errors.Wrap(ErrInvalidRequest, 0)
	}
	return redirectURI, nil
}

// MatchRedirectURIWithClientRedirectURIs if the given uri is a registered redirect uri. Does not perform
// uri validation.
//
// Considered specifications
// * http://tools.ietf.org/html/rfc6749#section-3.1.2.3
//   If multiple redirection URIs have been registered, if only part of
//   the redirection URI has been registered, or if no redirection URI has
//   been registered, the client MUST include a redirection URI with the
//   authorization request using the "redirect_uri" request parameter.
//
//   When a redirection URI is included in an authorization request, the
//   authorization server MUST compare and match the value received
//   against at least one of the registered redirection URIs (or URI
//   components) as defined in [RFC3986] Section 6, if any redirection
//   URIs were registered.  If the client registration included the full
//   redirection URI, the authorization server MUST compare the two URIs
//   using simple string comparison as defined in [RFC3986] Section 6.2.1.
//
// * https://tools.ietf.org/html/rfc6819#section-4.4.1.7
//   * The authorization server may also enforce the usage and validation
//     of pre-registered redirect URIs (see Section 5.2.3.5).  This will
//     allow for early recognition of authorization "code" disclosure to
//     counterfeit clients.
//   * The attacker will need to use another redirect URI for its
//     authorization process rather than the target web site because it
//     needs to intercept the flow.  So, if the authorization server
//     associates the authorization "code" with the redirect URI of a
//     particular end-user authorization and validates this redirect URI
//     with the redirect URI passed to the token's endpoint, such an
//     attack is detected (see Section 5.2.4.5).
func MatchRedirectURIWithClientRedirectURIs(rawurl string, client Client) (*url.URL, error) {
	if rawurl == "" && len(client.GetRedirectURIs()) == 1 {
		if redirectURIFromClient, err := url.Parse(client.GetRedirectURIs()[0]); err == nil && IsValidRedirectURI(redirectURIFromClient) {
			// If no redirect_uri was given and the client has exactly one valid redirect_uri registered, use that instead
			return redirectURIFromClient, nil
		}
	} else if rawurl != "" && StringInSlice(rawurl, client.GetRedirectURIs()) {
		// If a redirect_uri was given and the clients knows it (simple string comparison!)
		// return it.
		if parsed, err := url.Parse(rawurl); err == nil && IsValidRedirectURI(parsed) {
			// If no redirect_uri was given and the client has exactly one valid redirect_uri registered, use that instead
			return parsed, nil
		}
	}

	return nil, errors.New(ErrInvalidRequest)
}

// IsValidRedirectURI validates a redirect_uri as specified in:
//
// * https://tools.ietf.org/html/rfc6749#section-3.1.2
//   * The redirection endpoint URI MUST be an absolute URI as defined by [RFC3986] Section 4.3.
//   * The endpoint URI MUST NOT include a fragment component.
// * https://tools.ietf.org/html/rfc3986#section-4.3
//   absolute-URI  = scheme ":" hier-part [ "?" query ]
func IsValidRedirectURI(redirectURI *url.URL) bool {
	// We need to explicitly check for a scheme
	if !govalidator.IsRequestURL(redirectURI.String()) {
		return false
	}

	if redirectURI.Fragment != "" {
		// "The endpoint URI MUST NOT include a fragment component."
		return false
	}

	return true
}
