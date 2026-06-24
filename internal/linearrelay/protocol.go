// Package linearrelay implements the hosted relay that lets users plug h2 into
// Linear with one click. The relay is the single public endpoint for a
// published Linear OAuth app: it handles the OAuth install, holds each
// workspace's actor=app token, receives all agent-session webhooks, and routes
// each event to the user's locally-running h2 daemon over an outbound-dialed
// long-poll connection. The daemon therefore needs no inbound port, no tunnel,
// and never holds the Linear OAuth token.
//
// Wire protocol (daemon <-> relay), all JSON over HTTP:
//
//	GET  /poll?pair=<token>      -> 200 {event} | 204 (timeout, re-poll)
//	POST /activity?pair=<token>  <- {sessionId, activity} ; relay posts to Linear
//
// OAuth/install (browser <-> relay):
//
//	GET  /oauth/authorize        -> 302 to Linear
//	GET  /oauth/callback         -> exchanges code, shows the pairing token
//	POST /webhook                <- Linear agent-session events (signed)
package linearrelay

import "h2/internal/linear"

// ActivityEnvelope is the daemon -> relay request body for posting an activity.
type ActivityEnvelope struct {
	SessionID string               `json:"sessionId"`
	Activity  linear.AgentActivity `json:"activity"`
}
