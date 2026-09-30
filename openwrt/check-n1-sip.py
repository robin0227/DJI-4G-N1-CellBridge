#!/usr/bin/env python3
"""Local non-billable SIP smoke checks. Query REGISTER does not change binding."""
import hashlib
import json
import re
import socket
import uuid

with open('/mnt/mmcblk2p4/cellbridge/run/config.yaml') as handle:
    config = json.load(handle)
user = config['sip']['users'][0]
realm = config['sip']['realm']
uri = 'sip:127.0.0.1'
sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
sock.bind(('127.0.0.1', 0))
sock.settimeout(3)
call_id = 'local-smoke-' + uuid.uuid4().hex
sequence = 0

def request(method, authorization=None):
    global sequence
    sequence += 1
    lines = [
        '%s %s SIP/2.0' % (method, uri),
        'Via: SIP/2.0/UDP 127.0.0.1:%d;branch=z9hG4bK%s;rport' % (sock.getsockname()[1], uuid.uuid4().hex),
        'From: <sip:%s@127.0.0.1>;tag=local-smoke' % user['username'],
        'To: <sip:%s@127.0.0.1>' % user['username'],
        'Call-ID: ' + call_id,
        'CSeq: %d %s' % (sequence, method),
        'Max-Forwards: 70',
        'Content-Length: 0',
    ]
    if authorization:
        lines.append('Authorization: ' + authorization)
    sock.sendto(('\r\n'.join(lines) + '\r\n\r\n').encode(), ('127.0.0.1', 5060))
    reply = sock.recv(8192).decode()
    return int(reply.split()[1]), reply

def require(name, got, expected):
    if got != expected:
        raise RuntimeError('%s returned %d, expected %d' % (name, got, expected))
    print('%s: %d OK' % (name, got))

def digest(nonce, password):
    def md5(s):
        return hashlib.md5(s.encode()).hexdigest()
    cnonce = uuid.uuid4().hex
    ha1 = md5(user['username'] + ':' + realm + ':' + password)
    ha2 = md5('REGISTER:' + uri)
    response = md5(ha1 + ':' + nonce + ':00000001:' + cnonce + ':auth:' + ha2)
    return 'Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s", algorithm=MD5, qop=auth, nc=00000001, cnonce="%s"' % (user['username'], realm, nonce, uri, response, cnonce)

try:
    code, _ = request('OPTIONS')
    require('SIP OPTIONS', code, 200)
    code, reply = request('REGISTER')
    require('Missing authentication', code, 401)
    nonce = re.search(r'nonce="([^"]+)"', reply).group(1)
    code, _ = request('REGISTER', 'bogus')
    require('Fake authentication', code, 401)
    code, _ = request('REGISTER', digest(nonce, 'not-the-password'))
    require('Wrong password', code, 401)
    code, reply = request('REGISTER', digest(nonce, user['password']))
    require('Valid Digest registration query (no binding mutation)', code, 200)
    print('Handset binding currently present: %s' % ('yes' if '\r\nContact:' in reply else 'no'))
    code, _ = request('MESSAGE')  # Empty body; cannot cause SMS even on legacy code.
    require('Unregistered source MESSAGE', code, 403)
finally:
    sock.close()
