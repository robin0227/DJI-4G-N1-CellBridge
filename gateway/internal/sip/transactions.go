package sip

import (
	"fmt"
	"net"
	"strings"
	"time"
)

type inviteTransaction struct {
	req   string
	until time.Time
}

func transactionKey(callID string, remote *net.UDPAddr) string {
	return callID + "|" + remote.String()
}

func (s *Server) rememberInviteTransaction(req string, remote *net.UDPAddr) {
	s.inviteTransactions.Store(transactionKey(parseHeader(req, "Call-ID"), remote), inviteTransaction{req, time.Now().Add(inboundRingTimeout + 64*time.Second)})
}

func (s *Server) pruneInviteTransactions() {
	now := time.Now()
	s.inviteTransactions.Range(func(key, value any) bool {
		if !now.Before(value.(inviteTransaction).until) {
			s.inviteTransactions.Delete(key)
		}
		return true
	})
}

// Validate the source, CSeq and branch before a response can change a call.
// Recent transactions remain after Hangup so retransmitted 487s still get ACKs.
func (s *Server) inviteForResponse(resp string, remote *net.UDPAddr, sess *SIPCallSession) string {
	cseq := strings.Fields(parseHeader(resp, "CSeq"))
	if len(cseq) != 2 || cseq[1] != "INVITE" {
		return ""
	}
	req := ""
	if sess != nil {
		for _, attempt := range sess.InviteAttempts() {
			if sameUDPAddr(attempt.addr, remote) {
				req = attempt.req
				break
			}
		}
	}
	if req == "" {
		if value, ok := s.inviteTransactions.Load(transactionKey(parseHeader(resp, "Call-ID"), remote)); ok {
			transaction := value.(inviteTransaction)
			if time.Now().Before(transaction.until) {
				req = transaction.req
			}
		}
	}
	if req == "" || parseHeader(req, "CSeq") != parseHeader(resp, "CSeq") || viaBranchOf(req) != viaBranchOf(resp) || parseHeader(req, "From") != parseHeader(resp, "From") {
		return ""
	}
	return req
}

// Non-2xx ACK belongs to the INVITE transaction, hence the original Via branch
// and Request-URI (unlike the fresh-branch dialog ACK used for a 2xx).
func failedInviteACK(req, resp string) string {
	return fmt.Sprintf("ACK %s SIP/2.0\r\nVia: %s\r\nMax-Forwards: 70\r\nFrom: %s\r\nTo: %s\r\nCall-ID: %s\r\nCSeq: %d ACK\r\nContent-Length: 0\r\n\r\n", requestURIOf(req), parseHeader(req, "Via"), parseHeader(req, "From"), parseHeader(resp, "To"), parseHeader(req, "Call-ID"), cseqNumber(parseHeader(req, "CSeq")))
}

// A pickup racing our CANCEL creates a dialog even though the cellular leg is
// gone. ACK it, then BYE it instead of leaving a connected silent handset.
func (s *Server) closeLateAnswer(req, resp string, remote *net.UDPAddr) {
	s.sendACK(resp, remote, "")
	plan := byePlan{remote: remote, reqURI: contactURI(parseHeader(resp, "Contact"), remote, extractSIPUser(parseHeader(resp, "To"))), from: parseHeader(req, "From"), to: parseHeader(resp, "To"), callID: parseHeader(req, "Call-ID"), inviteCSeq: cseqNumber(parseHeader(req, "CSeq"))}
	_, message := teardownMessage("inbound", "active", "", plan, s.localIPFor(remote))
	if message != "" {
		_, _ = s.conn.WriteToUDP([]byte(message), remote)
	}
}
