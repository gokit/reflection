package nhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	htemplate "html/template"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gokit/npkg/nxid"
)

const (
	defaultMemory = 64 << 20 // 64 MB
)

// Render defines a giving type which exposes a Render method for
// rendering a custom output from a provided input string and bind
// object.
type Render interface {
	Render(io.Writer, string, interface{}) error
}

// Options defines a function type which receives a NContext pointer and
// sets/modifiers it's internal state values.
type Options func(*NContext)

// Apply applies giving options against NContext instance returning context again.
func Apply(c *NContext, ops ...Options) *NContext {
	for _, op := range ops {
		op(c)
	}

	return c
}

// SetID sets the id of the giving context.
func SetID(id nxid.ID) Options {
	return func(c *NContext) {
		c.id = id
	}
}

// SetMultipartFormSize sets the expected size for any multipart data.
func SetMultipartFormSize(size int64) Options {
	return func(c *NContext) {
		c.multipartFormSize = size
	}
}

// SetPath sets the path of the giving context.
func SetPath(p string) Options {
	return func(c *NContext) {
		c.path = p
	}
}

// SetRenderer will returns a function to set the render used by a giving context.
func SetRenderer(r Render) Options {
	return func(c *NContext) {
		c.render = r
	}
}

// SetResponseWriter returns a option function to set the response of a NContext.
func SetResponseWriter(w http.ResponseWriter, befores ...func()) Options {
	return func(c *NContext) {
		c.response = &Response{
			beforeFuncs: befores,
			Writer:      w,
		}
	}
}

// SetResponse returns a option function to set the response of a NContext.
func SetResponse(r *Response) Options {
	return func(c *NContext) {
		c.response = r
	}
}

// SetRequest returns a option function to set the request of a NContext.
func SetRequest(r *http.Request) Options {
	return func(c *NContext) {
		c.request = r
		c.ctx = r.Context()
		c.InitForms()
	}
}

// SetNotFound will return a function to set the NotFound handler for a giving context.
func SetNotFound(r ContextHandler) Options {
	return func(c *NContext) {
		c.notfoundHandler = r
	}
}

//=========================================================================================

// NContext defines a http related context object for a request session
// which is to be served.
type NContext struct {
	ctx context.Context

	multipartFormSize int64
	id                nxid.ID
	path              string
	render            Render
	response          *Response
	query             url.Values
	request           *http.Request
	flash             map[string][]string
	params            map[string]string
	notfoundHandler   ContextHandler
}

// NewContext returns a new NContext with the Options slice applied.
func NewContext(ops ...Options) *NContext {
	c := &NContext{
		id:     nxid.New(),
		flash:  map[string][]string{},
		params: map[string]string{},
	}

	if c.multipartFormSize <= 0 {
		c.multipartFormSize = defaultMemory
	}

	for _, op := range ops {
		if op == nil {
			continue
		}

		op(c)
	}

	return c
}

// ID returns the unique id of giving request context.
func (c *NContext) ID() nxid.ID {
	return c.id
}

// Context returns the underline context.NContext for the request.
func (c *NContext) Context() context.Context {
	return c.ctx
}

// Header returns the header associated with the giving request.
func (c *NContext) Header() http.Header {
	return c.request.Header
}

// GetHeader returns associated value of key from request headers.
func (c *NContext) GetHeader(key string) string {
	if c.request == nil {
		return ""
	}

	return c.request.Header.Get(key)
}

// AddHeader adds te value into the giving key into the response object header.
func (c *NContext) AddHeader(key string, value string) {
	if c.response == nil {
		return
	}

	c.response.Header().Add(key, value)
}

// AddParam adds a new parameter value into the NContext.
//
// This is not safe for concurrent use.
func (c *NContext) AddParam(key string, value string) {
	c.params[key] = value
}

// AddForm adds a new form value into the NContext.
//
// This is not safe for concurrent use.
func (c *NContext) Param(key string) string {
	return c.params[key]
}

// SetHeader sets te key-value pair into the response object header.
func (c *NContext) SetHeader(key string, value string) {
	if c.response == nil {
		return
	}

	c.response.Header().Set(key, value)
}

// HasHeader returns true/false if string.Contains validate giving header key
// has value within string of the request header.
// if value is an empty string, then method only validates that you
// have key in headers.
func (c *NContext) HasHeader(key string, value string) bool {
	if c.request == nil {
		return false
	}

	if value == "" {
		return c.request.Header.Get(key) != ""
	}

	return strings.Contains(c.request.Header.Get(key), value)
}

// Request returns the associated request.
func (c *NContext) Request() *http.Request {
	return c.request
}

// Body returns the associated io.ReadCloser which is the body of the Request.
func (c *NContext) Body() io.ReadCloser {
	return c.request.Body
}

// Response returns the associated response object for this context.
func (c *NContext) Response() *Response {
	return c.response
}

// IsTLS returns true/false if the giving reqest is a tls connection.
func (c *NContext) IsTLS() bool {
	return c.request.TLS != nil
}

// IsWebSocket returns true/false if the giving reqest is a websocket connection.
func (c *NContext) IsWebSocket() bool {
	upgrade := c.request.Header.Get(HeaderUpgrade)
	return upgrade == "websocket" || upgrade == "Websocket"
}

// Scheme attempts to return the exact url scheme of the request.
func (c *NContext) Scheme() string {
	// Can't use `r.Request.URL.Scheme`
	// See: https://groups.google.com/forum/#!topic/golang-nuts/pMUkBlQBDF0
	if c.IsTLS() {
		return "https"
	}
	if scheme := c.request.Header.Get(HeaderXForwardedProto); scheme != "" {
		return scheme
	}
	if scheme := c.request.Header.Get(HeaderXForwardedProtocol); scheme != "" {
		return scheme
	}
	if ssl := c.request.Header.Get(HeaderXForwardedSsl); ssl == "on" {
		return "https"
	}
	if scheme := c.request.Header.Get(HeaderXUrlScheme); scheme != "" {
		return scheme
	}
	return "http"
}

// RealIP attempts to return the ip of the giving request.
func (c *NContext) RealIP() string {
	ra := c.request.RemoteAddr
	if ip := c.request.Header.Get(HeaderXForwardedFor); ip != "" {
		ra = strings.Split(ip, ", ")[0]
	} else if ip := c.request.Header.Get(HeaderXRealIP); ip != "" {
		ra = ip
	} else {
		ra, _, _ = net.SplitHostPort(ra)
	}
	return ra
}

// Path returns the request path associated with the context.
func (c *NContext) Path() string {
	if c.path == "" && c.request != nil {
		c.path = c.request.URL.Path
	}

	return c.path
}

// QueryParam finds the giving value for the giving name in the querie set.
func (c *NContext) QueryParam(name string) string {
	if c.query == nil {
		c.query = c.request.URL.Query()
	}

	return c.query.Get(name)
}

// QueryParams returns the context url.Values object.
func (c *NContext) QueryParams() url.Values {
	if c.query == nil {
		c.query = c.request.URL.Query()
	}
	return c.query
}

// QueryString returns the raw query portion of the request path.
func (c *NContext) QueryString() string {
	return c.request.URL.RawQuery
}

// Form returns the url.Values of the giving request.
func (c *NContext) Form() url.Values {
	return c.request.Form
}

// FormValue returns the value of the giving item from the form fields.
func (c *NContext) FormValue(name string) string {
	return c.request.FormValue(name)
}

// FormParams returns a url.Values which contains the parse form values for
// multipart or wwww-urlencoded forms.
func (c *NContext) FormParams() (url.Values, error) {
	if strings.HasPrefix(c.request.Header.Get(HeaderContentType), MIMEMultipartForm) {
		if err := c.request.ParseMultipartForm(c.multipartFormSize); err != nil {
			return nil, err
		}
	} else {
		if err := c.request.ParseForm(); err != nil {
			return nil, err
		}
	}
	return c.request.Form, nil
}

// FormFile returns the giving FileHeader for the giving name.
func (c *NContext) FormFile(name string) (*multipart.FileHeader, error) {
	_, fh, err := c.request.FormFile(name)
	return fh, err
}

// MultipartForm returns the multipart form of the giving request if its a multipart
// request.
func (c *NContext) MultipartForm() (*multipart.Form, error) {
	err := c.request.ParseMultipartForm(defaultMemory)
	return c.request.MultipartForm, err
}

// Cookie returns the associated cookie by the giving name.
func (c *NContext) Cookie(name string) (*http.Cookie, error) {
	return c.request.Cookie(name)
}

// SetCookie sets the cookie into the response object.
func (c *NContext) SetCookie(cookie *http.Cookie) {
	http.SetCookie(c.response, cookie)
}

// Cookies returns the associated cookies slice of the http request.
func (c *NContext) Cookies() []*http.Cookie {
	return c.request.Cookies()
}

// ErrNoRenderInitiated defines the error returned when a renderer is not set
// but NContext.Render() is called.
var ErrNoRenderInitiated = errors.New("Renderer was not set or is uninitiated")

// Render renders the giving string with data binding using the provided Render
// of the context.
func (c *NContext) Render(code int, tmpl string, data interface{}) (err error) {
	if c.render == nil {
		return ErrNoRenderInitiated
	}

	buf := new(bytes.Buffer)

	if err = c.render.Render(buf, tmpl, data); err != nil {
		return
	}

	return c.HTMLBlob(code, buf.Bytes())
}

// Template renders provided template.Template object into the response object.
func (c *NContext) Template(code int, tmpl *template.Template, data interface{}) error {
	c.Status(code)
	return tmpl.Funcs(TextContextFunctions(c)).Execute(c.response, data)
}

// HTMLTemplate renders provided template.Template object into the response object.
func (c *NContext) HTMLTemplate(code int, tmpl *htemplate.Template, data interface{}) error {
	c.Status(code)
	return tmpl.Funcs(HTMLContextFunctions(c)).Execute(c.response, data)
}

// HTML renders giving html into response.
func (c *NContext) HTML(code int, html string) (err error) {
	return c.HTMLBlob(code, []byte(html))
}

// HTMLBlob renders giving html into response.
func (c *NContext) HTMLBlob(code int, b []byte) (err error) {
	return c.Blob(code, MIMETextHTMLCharsetUTF8, b)
}

// Error renders giving error response into response.
func (c *NContext) Error(statusCode int, errorCode string, message string, err error) error {
	c.response.Header().Set(HeaderContentType, MIMEApplicationJSONCharsetUTF8)
	return JSONError(c.Response(), statusCode, errorCode, message, err)
}

// String renders giving string into response.
func (c *NContext) String(code int, s string) (err error) {
	return c.Blob(code, MIMETextPlainCharsetUTF8, []byte(s))
}

// JSON renders giving json data into response.
func (c *NContext) JSON(code int, i interface{}) (err error) {
	_, pretty := c.QueryParams()["pretty"]
	if pretty {
		return c.JSONPretty(code, i, "  ")
	}
	b, err := json.Marshal(i)
	if err != nil {
		return
	}
	return c.JSONBlob(code, b)
}

// JSONPretty renders giving json data as indented into response.
func (c *NContext) JSONPretty(code int, i interface{}, indent string) (err error) {
	b, err := json.MarshalIndent(i, "", indent)
	if err != nil {
		return
	}
	return c.JSONBlob(code, b)
}

// JSONBlob renders giving json data into response with proper mime type.
func (c *NContext) JSONBlob(code int, b []byte) (err error) {
	return c.Blob(code, MIMEApplicationJSONCharsetUTF8, b)
}

// JSONP renders giving jsonp as response with proper mime type.
func (c *NContext) JSONP(code int, callback string, i interface{}) (err error) {
	b, err := json.Marshal(i)
	if err != nil {
		return
	}
	return c.JSONPBlob(code, callback, b)
}

// JSONPBlob renders giving jsonp as response with proper mime type.
func (c *NContext) JSONPBlob(code int, callback string, b []byte) (err error) {
	c.response.Header().Set(HeaderContentType, MIMEApplicationJavaScriptCharsetUTF8)
	c.response.WriteHeader(code)
	if _, err = c.response.Write([]byte(callback + "(")); err != nil {
		return
	}
	if _, err = c.response.Write(b); err != nil {
		return
	}
	_, err = c.response.Write([]byte(");"))
	return
}

// XML renders giving xml as response with proper mime type.
func (c *NContext) XML(code int, i interface{}) (err error) {
	_, pretty := c.QueryParams()["pretty"]
	if pretty {
		return c.XMLPretty(code, i, "  ")
	}
	b, err := xml.Marshal(i)
	if err != nil {
		return
	}
	return c.XMLBlob(code, b)
}

// XMLPretty renders giving xml as indent as response with proper mime type.
func (c *NContext) XMLPretty(code int, i interface{}, indent string) (err error) {
	b, err := xml.MarshalIndent(i, "", indent)
	if err != nil {
		return
	}
	return c.XMLBlob(code, b)
}

// XMLBlob renders giving xml as response with proper mime type.
func (c *NContext) XMLBlob(code int, b []byte) (err error) {
	c.response.Header().Set(HeaderContentType, MIMEApplicationXMLCharsetUTF8)
	c.response.WriteHeader(code)
	if _, err = c.response.Write([]byte(xml.Header)); err != nil {
		return
	}
	_, err = c.response.Write(b)
	return
}

// Blob write giving byte slice as response with proper mime type.
func (c *NContext) Blob(code int, contentType string, b []byte) (err error) {
	c.response.Header().Set(HeaderContentType, contentType)
	c.response.WriteHeader(code)
	_, err = c.response.Write(b)
	return
}

// Stream copies giving io.Readers content into response.
func (c *NContext) Stream(code int, contentType string, r io.Reader) (err error) {
	c.response.Header().Set(HeaderContentType, contentType)
	c.response.WriteHeader(code)
	_, err = io.Copy(c.response, r)
	return
}

// File streams file content into response.
func (c *NContext) File(file string) (err error) {
	f, err := os.Open(file)
	if err != nil {
		return err
	}

	defer f.Close()

	fi, _ := f.Stat()
	if fi.IsDir() {
		file = filepath.Join(file, "index.html")
		f, err = os.Open(file)
		if err != nil {
			return
		}

		defer f.Close()
		if fi, err = f.Stat(); err != nil {
			return
		}
	}

	http.ServeContent(c.Response(), c.Request(), fi.Name(), fi.ModTime(), f)
	return
}

// Attachment attempts to attach giving file details.
func (c *NContext) Attachment(file, name string) (err error) {
	return c.contentDisposition(file, name, "attachment")
}

// Inline attempts to inline file content.
func (c *NContext) Inline(file, name string) (err error) {
	return c.contentDisposition(file, name, "inline")
}

// SetFlash sets giving message/messages into the slice bucket of the
// given name list.
func (c *NContext) SetFlash(name string, message string) {
	c.flash[name] = append(c.flash[name], message)
}

// ClearFlashMessages clears all available message items within
// the flash map.
func (c *NContext) ClearFlashMessages() {
	c.flash = make(map[string][]string)
}

// FlashMessages returns available map of all flash messages.
// A copy is sent not the context currently used instance.
func (c *NContext) FlashMessages() map[string][]string {
	copy := make(map[string][]string)
	for name, messages := range c.flash {
		copy[name] = append([]string{}, messages...)
	}
	return copy
}

// ClearFlash removes all available message items from the context flash message
// map.
func (c *NContext) ClearFlash(name string) {
	if _, ok := c.flash[name]; ok {
		c.flash[name] = nil
	}
}

// Flash returns an associated slice of messages/string, for giving
// flash name/key.
func (c *NContext) Flash(name string) []string {
	messages := c.flash[name]
	return messages
}

// ModContext executes provided function against current context
// modifying the current context with the returned and updating
// underlying request with new context NContext.
//
// It is not safe for concurrent use.
func (c *NContext) ModContext(modder func(ctx context.Context) context.Context) {
	if c.ctx == nil {
		return
	}

	var newCtx = modder(c.ctx)
	c.request = c.request.WithContext(newCtx)
	c.ctx = c.request.Context()
}

// NotFound writes calls the giving response against the NotFound handler
// if present, else uses a http.StatusMovedPermanently status code.
func (c *NContext) NotFound() error {
	if c.notfoundHandler != nil {
		return c.notfoundHandler(c)
	}

	c.response.WriteHeader(http.StatusMovedPermanently)
	return nil
}

// Status writes status code without writing content to response.
func (c *NContext) Status(code int) {
	c.response.WriteHeader(code)
}

// NoContent writes status code without writing content to response.
func (c *NContext) NoContent(code int) error {
	c.response.WriteHeader(code)
	return nil
}

// ErrInvalidRedirectCode is error returned when redirect code is wrong.
var ErrInvalidRedirectCode = errors.New("Invalid redirect code")

// Redirect redirects context response.
func (c *NContext) Redirect(code int, url string) error {
	if code < 300 || code > 308 {
		return ErrInvalidRedirectCode
	}

	c.response.Header().Set(HeaderLocation, url)
	c.response.WriteHeader(code)
	return nil
}

// InitForms will call the appropriate function to parse the necessary form values
// within the giving request context.
func (c *NContext) InitForms() error {
	if c.request == nil {
		return nil
	}

	if _, err := c.FormParams(); err != nil {
		return err
	}
	return nil
}

// Reset resets context internal fields
func (c *NContext) Reset(r *http.Request, w http.ResponseWriter) error {
	if r == nil && w == nil {
		c.request = nil
		c.response = nil
		c.query = nil
		c.notfoundHandler = nil
		c.params = nil
		c.flash = nil
		c.ctx = nil
		return nil
	}

	c.request = r
	c.query = nil
	c.id = nxid.New()
	c.ctx = r.Context()
	c.notfoundHandler = nil
	c.params = map[string]string{}
	c.flash = map[string][]string{}

	if c.multipartFormSize <= 0 {
		c.multipartFormSize = defaultMemory
	}

	c.request = r
	c.response = &Response{Writer: w}
	return c.InitForms()
}

func (c *NContext) contentDisposition(file, name, dispositionType string) (err error) {
	c.response.Header().Set(HeaderContentDisposition, fmt.Sprintf("%s; filename=%s", dispositionType, name))
	c.File(file)
	return
}
