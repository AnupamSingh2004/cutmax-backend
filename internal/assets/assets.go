// Package assets embeds small static binary assets (currently just the
// company logo mark) needed by server-generated documents like enquiry PDFs,
// so the backend doesn't depend on reaching into the frontend's public dir.
package assets

import _ "embed"

//go:embed logo.png
var LogoPNG []byte
