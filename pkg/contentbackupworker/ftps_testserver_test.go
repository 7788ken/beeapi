package contentbackupworker

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Synthetic in-memory FTPS server for T04 tests. It implements only the command
// subset the adapter uses and can inject the failures listed in the task card.
// Its RNTO refuses to replace an existing target by default; that is this
// fake's documented semantic, not proof of the real target's behavior.
type fakeFTPSCfg struct {
	cert     tls.Certificate
	username string
	password string

	refuseAuth       bool
	refuseProt       bool
	plaintextData    bool
	corruptAfterSTOR bool
	mkdDenied        bool
	rejectRename     bool
	refuseDelete     bool
	rnToOverwrite    bool
	refuseEPSV       bool
	listOnly         bool   // FEAT without MLST, so clients fall back to LIST (vsftpd has no MLSD)
	hangPhase        string // greeting | auth-tls | login | stor | retr | mlsd; copied to the runtime field

	files map[string][]byte
	dirs  map[string]bool

	// rnToConflict simulates a concurrent worker winning the race between the
	// client's pre-rename existence check and RNTO. It may plant the target
	// file; returning true makes the server refuse the rename with 550.
	rnToConflict func(s *fakeFTPS, from, to string) bool
}

type fakeFTPS struct {
	t   *testing.T
	cfg fakeFTPSCfg

	ln        net.Listener
	port      int
	pin       string
	tlsConfig *tls.Config

	mu       sync.Mutex
	hang     string
	files    map[string][]byte
	dirs     map[string]bool
	events   []string
	liveConn map[net.Conn]bool
	// mtimes overrides the MLSD Modify fact per file; others list the fixed default.
	mtimes map[string]time.Time

	controlClosed    chan struct{}
	controlCloseOnce sync.Once
	dataClosed       chan struct{}
	dataCloseOnce    sync.Once

	wg sync.WaitGroup
}

func cbGenerateCert(t *testing.T, notBefore, notAfter time.Time) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "synthetic-ftps.local"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost", "synthetic-ftps.local"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, hex.EncodeToString(sum[:])
}

func cbStartFTPS(t *testing.T, mutate func(cfg *fakeFTPSCfg)) *fakeFTPS {
	t.Helper()
	cert, _ := cbGenerateCert(t, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	cfg := fakeFTPSCfg{
		cert:     cert,
		username: "cbuser",
		password: "cbpass",
	}
	if mutate != nil {
		mutate(&cfg)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeFTPS{
		t:             t,
		cfg:           cfg,
		ln:            ln,
		port:          ln.Addr().(*net.TCPAddr).Port,
		hang:          cfg.hangPhase,
		tlsConfig:     &tls.Config{Certificates: []tls.Certificate{cfg.cert}, MinVersion: tls.VersionTLS12},
		files:         map[string][]byte{},
		dirs:          map[string]bool{"/": true},
		liveConn:      map[net.Conn]bool{},
		mtimes:        map[string]time.Time{},
		controlClosed: make(chan struct{}),
		dataClosed:    make(chan struct{}),
	}
	leafSum := sha256.Sum256(cfg.cert.Certificate[0])
	s.pin = hex.EncodeToString(leafSum[:])
	for p, data := range cfg.files {
		s.files[p] = append([]byte(nil), data...)
	}
	for d := range cfg.dirs {
		s.dirs[d] = true
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.trackConn(conn, true)
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.handleControl(conn)
			}()
		}
	}()
	t.Cleanup(s.close)
	return s
}

func (s *fakeFTPS) close() {
	s.ln.Close()
	s.mu.Lock()
	for c := range s.liveConn {
		c.Close()
	}
	s.liveConn = map[net.Conn]bool{}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.t.Error("fake FTPS server did not shut down within 5s")
	}
}

func (s *fakeFTPS) trackConn(c net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.liveConn[c] = true
	} else {
		delete(s.liveConn, c)
	}
}

func (s *fakeFTPS) record(event string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *fakeFTPS) currentHang() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hang
}

func (s *fakeFTPS) setHang(phase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hang = phase
}

func (s *fakeFTPS) eventSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

func (s *fakeFTPS) hasEvent(substr string) bool {
	for _, e := range s.eventSnapshot() {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

func (s *fakeFTPS) countEvents(substr string) int {
	n := 0
	for _, e := range s.eventSnapshot() {
		if strings.Contains(e, substr) {
			n++
		}
	}
	return n
}

func (s *fakeFTPS) fileSnapshot(p string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[p]
	return append([]byte(nil), data...), ok
}

func (s *fakeFTPS) listPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.files))
	for p := range s.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// seedFile plants a file as if a previous or concurrent worker had produced it.
func (s *fakeFTPS) seedFile(p string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[p] = append([]byte(nil), data...)
	dir := path.Dir(p)
	for dir != "/" && dir != "." {
		s.dirs[dir] = true
		dir = path.Dir(dir)
	}
	s.dirs["/"] = true
}

// seedFileAt is seedFile with the Modify time MLSD reports for it.
func (s *fakeFTPS) seedFileAt(p string, data []byte, mtime time.Time) {
	s.seedFile(p, data)
	s.mu.Lock()
	s.mtimes[p] = mtime
	s.mu.Unlock()
}

// seedDir plants a directory and its parents.
func (s *fakeFTPS) seedDir(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for dir := p; dir != "/" && dir != "."; dir = path.Dir(dir) {
		s.dirs[dir] = true
	}
}

type fakeFTPSConn struct {
	srv        *fakeFTPS
	conn       net.Conn
	rd         *bufio.Reader
	user       string
	authed     bool
	protP      bool
	cwd        string
	renameFrom string
	dataLn     net.Listener
}

func (c *fakeFTPSConn) writeLine(line string) {
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c.conn, line+"\r\n")
}

func (c *fakeFTPSConn) resolve(p string) string {
	if strings.HasPrefix(p, "/") {
		return path.Clean(p)
	}
	return path.Clean(path.Join(c.cwd, p))
}

func (c *fakeFTPSConn) closeDataLn() {
	if c.dataLn != nil {
		c.dataLn.Close()
		c.dataLn = nil
	}
}

func (c *fakeFTPSConn) acceptData() (net.Conn, error) {
	if c.dataLn == nil {
		return nil, fmt.Errorf("no passive listener")
	}
	tln, ok := c.dataLn.(*net.TCPListener)
	if !ok {
		return nil, fmt.Errorf("unexpected listener type")
	}
	tln.SetDeadline(time.Now().Add(5 * time.Second))
	conn, err := tln.Accept()
	c.closeDataLn()
	if err != nil {
		return nil, err
	}
	c.srv.trackConn(conn, true)
	return conn, nil
}

func (s *fakeFTPS) handleControl(raw net.Conn) {
	defer func() {
		raw.Close()
		s.trackConn(raw, false)
		s.controlCloseOnce.Do(func() { close(s.controlClosed) })
	}()
	c := &fakeFTPSConn{srv: s, conn: raw, rd: bufio.NewReader(raw), cwd: "/"}
	defer c.closeDataLn()
	if s.currentHang() == "greeting" {
		s.record("HANG-GREETING")
		s.blockUntilClosed(raw)
		return
	}
	c.writeLine("220 synthetic FTPS ready")
	for {
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		line, err := c.rd.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		cmd := strings.ToUpper(parts[0])
		arg := ""
		if len(parts) == 2 {
			arg = parts[1]
		}
		if cmd == "PASS" {
			s.record("PASS <redacted>")
		} else {
			s.record(line)
		}
		if !c.dispatch(cmd, arg) {
			return
		}
	}
}

// dispatch returns false when the connection is done.
func (c *fakeFTPSConn) dispatch(cmd, arg string) bool {
	s := c.srv
	switch cmd {
	case "AUTH":
		if arg != "TLS" {
			c.writeLine("500 Unknown AUTH mode.")
			return true
		}
		if s.currentHang() == "auth-tls" {
			s.record("HANG-AUTH-TLS")
			s.blockUntilClosed(c.conn)
			return false
		}
		c.writeLine("234 Proceed with negotiation.")
		tlsConn := tls.Server(c.conn, s.tlsConfig)
		if err := tlsConn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return false
		}
		if err := tlsConn.Handshake(); err != nil {
			s.record("CONTROL-HANDSHAKE-FAILED")
			return false
		}
		tlsConn.SetDeadline(time.Time{})
		c.conn = tlsConn
		c.rd = bufio.NewReader(tlsConn)
		s.record("CONTROL-HANDSHAKE-OK")
		return true
	case "USER":
		if s.currentHang() == "login" {
			s.record("HANG-LOGIN")
			s.blockUntilClosed(c.conn)
			return false
		}
		c.user = arg
		c.writeLine("331 Please specify the password.")
		return true
	case "PASS":
		if s.cfg.refuseAuth || arg != s.cfg.password {
			c.writeLine("530 Login incorrect.")
			return true
		}
		c.authed = true
		c.writeLine("230 Login successful.")
		return true
	case "FEAT":
		c.writeLine("211-Features:")
		c.writeLine(" EPSV")
		c.writeLine(" PASV")
		if !s.cfg.listOnly {
			c.writeLine(" MLST")
		}
		c.writeLine(" SIZE")
		c.writeLine(" AUTH TLS")
		c.writeLine(" PROT P")
		c.writeLine("211 End")
		return true
	case "PBSZ":
		c.writeLine("200 PBSZ set to 0.")
		return true
	case "PROT":
		if s.cfg.refuseProt {
			c.writeLine("500 PROT is not allowed here.")
			return true
		}
		if arg == "P" {
			c.protP = true
			c.writeLine("200 Protection set to Private.")
		} else {
			c.protP = false
			c.writeLine("200 Protection set to Clear.")
		}
		return true
	case "TYPE":
		c.writeLine("200 Switching to Binary mode.")
		return true
	case "OPTS":
		c.writeLine("200 Always in UTF8 mode.")
		return true
	case "PWD":
		c.writeLine(fmt.Sprintf("257 %q is the current directory.", c.cwd))
		return true
	case "CWD":
		p := c.resolve(arg)
		s.mu.Lock()
		ok := s.dirs[p]
		s.mu.Unlock()
		if ok {
			c.cwd = p
			c.writeLine("250 Directory successfully changed.")
		} else {
			c.writeLine("550 Failed to change directory.")
		}
		return true
	case "MKD":
		p := c.resolve(arg)
		if s.cfg.mkdDenied {
			c.writeLine("550 Permission denied.")
			return true
		}
		s.mu.Lock()
		exists := s.dirs[p]
		if !exists {
			s.dirs[p] = true
		}
		s.mu.Unlock()
		if exists {
			c.writeLine("550 Directory already exists.")
		} else {
			c.writeLine(fmt.Sprintf("257 %q created.", p))
		}
		return true
	case "SIZE":
		p := c.resolve(arg)
		s.mu.Lock()
		data, ok := s.files[p]
		size := len(data)
		s.mu.Unlock()
		if ok {
			c.writeLine(fmt.Sprintf("213 %d", size))
		} else {
			c.writeLine("550 Could not get file size.")
		}
		return true
	case "EPSV":
		if s.cfg.refuseEPSV {
			c.writeLine("500 EPSV not understood.")
			return true
		}
		c.openPassiveListener("229 Entering Extended Passive Mode (|||%d|).")
		return true
	case "PASV":
		c.openPassiveListener("227 Entering Passive Mode (127,0,0,1,%d/256,%d/256).")
		return true
	case "STOR":
		c.handleSTOR(arg)
		return true
	case "RETR":
		c.handleRETR(arg)
		return true
	case "MLSD":
		c.handleListing(arg, true)
		return true
	case "LIST":
		c.handleListing(arg, false)
		return true
	case "RNFR":
		if s.cfg.rejectRename {
			c.writeLine("502 Command not implemented.")
			return true
		}
		p := c.resolve(arg)
		s.mu.Lock()
		_, ok := s.files[p]
		s.mu.Unlock()
		if !ok {
			c.writeLine("550 File or directory not found.")
			return true
		}
		c.renameFrom = p
		c.writeLine("350 Ready for RNTO.")
		return true
	case "RNTO":
		if s.cfg.rejectRename {
			c.writeLine("502 Command not implemented.")
			return true
		}
		if c.renameFrom == "" {
			c.writeLine("503 Bad sequence of commands.")
			return true
		}
		from := c.renameFrom
		c.renameFrom = ""
		p := c.resolve(arg)
		s.mu.Lock()
		_, targetExists := s.files[p]
		s.mu.Unlock()
		if !targetExists && s.cfg.rnToConflict != nil && s.cfg.rnToConflict(s, from, p) {
			s.record("RNTO-REFUSED-RACE " + p)
			c.writeLine("550 Target file already exists.")
			return true
		}
		s.mu.Lock()
		_, targetExists = s.files[p]
		if targetExists && !s.cfg.rnToOverwrite {
			s.mu.Unlock()
			s.record("RNTO-REFUSED-EXISTING " + p)
			c.writeLine("550 Target file already exists.")
			return true
		}
		body := s.files[from]
		delete(s.files, from)
		s.files[p] = body
		s.mu.Unlock()
		s.record("RENAMED " + from + " -> " + p)
		c.writeLine("250 Rename successful.")
		return true
	case "DELE":
		p := c.resolve(arg)
		if s.cfg.refuseDelete {
			s.record("DELE-DENIED " + p)
			c.writeLine("550 Permission denied.")
			return true
		}
		s.mu.Lock()
		_, ok := s.files[p]
		if ok {
			delete(s.files, p)
		}
		s.mu.Unlock()
		if ok {
			s.record("DELETED " + p)
			c.writeLine("250 Delete successful.")
		} else {
			c.writeLine("550 File not found.")
		}
		return true
	case "NOOP":
		c.writeLine("200 NOOP ok.")
		return true
	case "QUIT":
		c.writeLine("221 Goodbye.")
		return false
	default:
		s.record("UNKNOWN-CMD " + cmd)
		c.writeLine("500 Unknown command.")
		return true
	}
}

func (c *fakeFTPSConn) openPassiveListener(format string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.writeLine("425 Cannot open passive connection.")
		return
	}
	c.closeDataLn()
	c.dataLn = ln
	port := ln.Addr().(*net.TCPAddr).Port
	if strings.Contains(format, "/256") {
		c.writeLine(fmt.Sprintf("227 Entering Passive Mode (127,0,0,1,%d,%d).", port/256, port%256))
	} else {
		c.writeLine(fmt.Sprintf(format, port))
	}
}

func (c *fakeFTPSConn) handleSTOR(arg string) {
	s := c.srv
	p := c.resolve(arg)
	if s.currentHang() == "stor" {
		// Accept the pending data connection first so the test can also
		// observe the client closing it on cancellation.
		if dc, err := c.acceptData(); err == nil {
			s.trackConn(dc, true)
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer dc.Close()
				defer s.trackConn(dc, false)
				buf := make([]byte, 256)
				for {
					if _, err := dc.Read(buf); err != nil {
						s.record("DATA-CONN-CLOSED")
						s.dataCloseOnce.Do(func() { close(s.dataClosed) })
						return
					}
				}
			}()
		}
		s.record("HANG-STOR")
		s.blockUntilClosed(c.conn)
		return
	}
	c.writeLine("150 Ok to send data.")
	dc, err := c.acceptData()
	if err != nil {
		s.record("STOR-ACCEPT-FAILED")
		c.writeLine("425 Cannot open data connection.")
		return
	}
	defer func() { dc.Close(); s.trackConn(dc, false) }()
	body, refuse, err := s.readDataBody(dc, c.protP)
	if err != nil || refuse {
		s.record("STOR-REFUSED-BODY")
		c.writeLine("426 Transfer failed.")
		return
	}
	if s.cfg.corruptAfterSTOR && len(body) > 0 {
		body[0] ^= 0xFF
	}
	s.mu.Lock()
	s.files[p] = body
	s.mu.Unlock()
	s.record("STOR-COMPLETED " + p)
	c.writeLine("226 Transfer complete.")
}

// readDataBody returns refuse=true when a private channel was negotiated but
// the peer did not complete a TLS handshake: the fake then stores nothing,
// which is how the "no plaintext data channel" assertions are made.
func (s *fakeFTPS) readDataBody(dc net.Conn, protP bool) ([]byte, bool, error) {
	if protP && s.cfg.plaintextData {
		dc.SetReadDeadline(time.Now().Add(3 * time.Second))
		first := make([]byte, 1)
		if _, err := dc.Read(first); err != nil {
			return nil, true, nil
		}
		s.record("DATA-TLS-CLIENTHELLO-DISCARDED")
		io.Copy(io.Discard, dc)
		return nil, true, nil
	}
	var src io.Reader = dc
	if protP {
		tlsConn := tls.Server(dc, s.tlsConfig)
		if err := tlsConn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return nil, false, err
		}
		src = tlsConn
	}
	data, err := io.ReadAll(src)
	return data, false, err
}

func (c *fakeFTPSConn) handleRETR(arg string) {
	s := c.srv
	if s.currentHang() == "retr" {
		s.record("HANG-RETR")
		s.blockUntilClosed(c.conn)
		return
	}
	p := c.resolve(arg)
	s.mu.Lock()
	data, ok := s.files[p]
	s.mu.Unlock()
	if !ok {
		c.writeLine("550 File not found.")
		return
	}
	c.writeLine("150 Opening BINARY mode data connection.")
	dc, err := c.acceptData()
	if err != nil {
		s.record("RETR-ACCEPT-FAILED")
		c.writeLine("425 Cannot open data connection.")
		return
	}
	defer func() { dc.Close(); s.trackConn(dc, false) }()
	if c.protP {
		if s.cfg.plaintextData {
			s.record("DATA-PLAINTEXT-WRITE")
			dc.SetWriteDeadline(time.Now().Add(3 * time.Second))
			dc.Write(data)
			c.writeLine("226 Transfer complete.")
			return
		}
		tlsConn := tls.Server(dc, s.tlsConfig)
		tlsConn.SetDeadline(time.Now().Add(10 * time.Second))
		dc = tlsConn
	}
	dc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := dc.Write(data); err != nil {
		s.record("RETR-WRITE-FAILED")
		c.writeLine("426 Transfer failed.")
		return
	}
	if tc, ok := dc.(*tls.Conn); ok {
		tc.CloseWrite()
	}
	s.record("RETR-COMPLETED " + p)
	c.writeLine("226 Transfer complete.")
}

// handleListing serves MLSD (RFC 3659 facts, UTC) or, for mlsd=false, a Unix ls style LIST
// whose times carry no year, like vsftpd.
func (c *fakeFTPSConn) handleListing(arg string, mlsd bool) {
	s := c.srv
	if s.currentHang() == "mlsd" {
		s.record("HANG-MLSD")
		s.blockUntilClosed(c.conn)
		return
	}
	dir := c.resolve(arg)
	prefix := dir
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	type entry struct {
		name string
		typ  string
		size int
		at   time.Time
	}
	defaultAt := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	var entries []entry
	s.mu.Lock()
	// Like ProFTPD and Pure-FTPd, a directory that does not exist is a 550, not an empty list.
	if !s.dirs[dir] {
		s.mu.Unlock()
		c.closeDataLn()
		s.record("MLSD-MISSING " + dir)
		c.writeLine("550 No such directory.")
		return
	}
	for p, data := range s.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := p[len(prefix):]
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		at := defaultAt
		if mtime, ok := s.mtimes[p]; ok {
			at = mtime
		}
		entries = append(entries, entry{name: rest, typ: "file", size: len(data), at: at})
	}
	for d := range s.dirs {
		if d == dir || !strings.HasPrefix(d, prefix) {
			continue
		}
		rest := d[len(prefix):]
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		entries = append(entries, entry{name: rest, typ: "dir", at: defaultAt})
	}
	s.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	c.writeLine("150 Opening data connection for directory list.")
	dc, err := c.acceptData()
	if err != nil {
		c.writeLine("425 Cannot open data connection.")
		return
	}
	defer func() { dc.Close(); s.trackConn(dc, false) }()
	var out io.Writer = dc
	var tlsOut *tls.Conn
	if c.protP {
		tlsOut = tls.Server(dc, s.tlsConfig)
		tlsOut.SetDeadline(time.Now().Add(10 * time.Second))
		out = tlsOut
	}
	w := bufio.NewWriter(out)
	for _, e := range entries {
		if mlsd {
			fmt.Fprintf(w, "Type=%s;Size=%d;Modify=%s; %s\r\n", e.typ, e.size, e.at.UTC().Format("20060102150405"), e.name)
			continue
		}
		perms := "-rw-r--r--"
		if e.typ == "dir" {
			perms = "drwxr-xr-x"
		}
		fmt.Fprintf(w, "%s 1 ftp ftp %d %s %s\r\n", perms, e.size, e.at.UTC().Format("Jan _2 15:04"), e.name)
	}
	if err := w.Flush(); err != nil {
		c.writeLine("426 Transfer failed.")
		return
	}
	if tlsOut != nil {
		tlsOut.CloseWrite()
	} else {
		dc.(*net.TCPConn).CloseWrite()
	}
	s.record("MLSD-COMPLETED " + dir)
	c.writeLine("226 Transfer complete.")
}

func (s *fakeFTPS) blockUntilClosed(conn net.Conn) {
	buf := make([]byte, 512)
	for {
		conn.SetReadDeadline(time.Time{})
		if _, err := conn.Read(buf); err != nil {
			return
		}
	}
}
