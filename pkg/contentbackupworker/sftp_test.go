package contentbackupworker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// 真实目标只支持 TLS 1.0 的 FTPS（文档 8.2，2026-09-18 探测），可用的加密通道是它的
// SSH/SFTP 端口。下面用进程内的 SSH + SFTP 服务器把 SFTP 适配器按与 FTPS 同一份完成协议
// 逐条证伪：pin 先于认证、读回校验、幂等/冲突、缺失、取消、错误归类。

type fakeSFTP struct {
	t        *testing.T
	listener net.Listener
	port     int
	signer   ssh.Signer
	pin      string
	// 服务器同时持有一把 ECDSA 主机密钥：真实目标 5.45.76.50 有 rsa/ecdsa/ed25519 三把，
	// 客户端的算法偏好决定服务器拿哪把出来，pin 必须与之一致。
	ecdsaPin string
	user     string
	pass     string
	handlers sftp.Handlers
	direct   *sftp.Client // 测试自己的旁路连接，用来预置/检查文件

	authAttempts atomic.Int64
	openConns    atomic.Int64
	accepts      atomic.Int64
	mu           sync.Mutex
	corrupt      map[string]bool
	closed       chan struct{}
	// startDir 模拟未 chroot 的账号：真实目标登录后落在 /raid/backup/b_459494，"/" 是真根。
	startDir string
}

// corruptingReads 让指定路径的读取返回被翻转的首字节：模拟远端落盘损坏或传输错误。
type corruptingReads struct {
	inner sftp.FileReader
	srv   *fakeSFTP
}

func (c *corruptingReads) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	ra, err := c.inner.Fileread(r)
	if err != nil {
		return nil, err
	}
	c.srv.mu.Lock()
	bad := c.srv.corrupt[r.Filepath]
	c.srv.mu.Unlock()
	if !bad {
		return ra, nil
	}
	return &flipFirstByte{ra}, nil
}

type flipFirstByte struct{ io.ReaderAt }

func (f *flipFirstByte) ReadAt(p []byte, off int64) (int, error) {
	n, err := f.ReaderAt.ReadAt(p, off)
	if off == 0 && n > 0 {
		p[0] ^= 0xff
	}
	return n, err
}

func cbStartSFTP(t *testing.T) *fakeSFTP {
	return cbStartSFTPAt(t, "/")
}

func cbStartSFTPAt(t *testing.T, startDir string) *fakeSFTP {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	root := sftp.InMemHandler()
	srv := &fakeSFTP{
		t:        t,
		listener: listener,
		port:     listener.Addr().(*net.TCPAddr).Port,
		signer:   signer,
		pin:      HostKeySHA256Hex(signer.PublicKey()),
		user:     "cbuser",
		pass:     "cbpass",
		corrupt:  map[string]bool{},
		closed:   make(chan struct{}),
		startDir: startDir,
	}
	srv.handlers = sftp.Handlers{
		FileGet:  &corruptingReads{inner: root.FileGet, srv: srv},
		FilePut:  root.FilePut,
		FileCmd:  root.FileCmd,
		FileList: root.FileList,
	}
	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			srv.authAttempts.Add(1)
			if conn.User() == srv.user && string(password) == srv.pass {
				return nil, nil
			}
			return nil, errors.New("wrong credentials")
		},
	}
	config.AddHostKey(signer)
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa host key: %v", err)
	}
	ecdsaSigner, err := ssh.NewSignerFromKey(ecdsaKey)
	if err != nil {
		t.Fatalf("ecdsa signer: %v", err)
	}
	config.AddHostKey(ecdsaSigner)
	srv.ecdsaPin = HostKeySHA256Hex(ecdsaSigner.PublicKey())
	go srv.serve(config)
	t.Cleanup(func() {
		close(srv.closed)
		_ = listener.Close()
		if srv.direct != nil {
			_ = srv.direct.Close()
		}
	})
	srv.direct = srv.dialDirect(t)
	return srv
}

func (srv *fakeSFTP) serve(config *ssh.ServerConfig) {
	for {
		conn, err := srv.listener.Accept()
		if err != nil {
			return
		}
		srv.accepts.Add(1)
		go func(conn net.Conn) {
			srv.openConns.Add(1)
			defer srv.openConns.Add(-1)
			defer conn.Close()
			sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
			if err != nil {
				return
			}
			defer sshConn.Close()
			go ssh.DiscardRequests(reqs)
			for newChan := range chans {
				if newChan.ChannelType() != "session" {
					_ = newChan.Reject(ssh.UnknownChannelType, "only session")
					continue
				}
				channel, requests, err := newChan.Accept()
				if err != nil {
					return
				}
				go func(channel ssh.Channel, requests <-chan *ssh.Request) {
					for req := range requests {
						ok := req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp"
						_ = req.Reply(ok, nil)
						if !ok {
							continue
						}
						server := sftp.NewRequestServer(channel, srv.handlers, sftp.WithStartDirectory(srv.startDir))
						_ = server.Serve()
						_ = server.Close()
						return
					}
				}(channel, requests)
			}
		}(conn)
	}
}

// dialDirect 是测试自己的 SFTP 连接：不经过被测适配器，用来预置文件、读内容、列目录。
func (srv *fakeSFTP) dialDirect(t *testing.T) *sftp.Client {
	t.Helper()
	client, err := ssh.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(srv.port)), &ssh.ClientConfig{
		User:            srv.user,
		Auth:            []ssh.AuthMethod{ssh.Password(srv.pass)},
		HostKeyCallback: ssh.FixedHostKey(srv.signer.PublicKey()),
		// 旁路连接也得点名 ed25519：库默认偏好 ECDSA，服务器会拿另一把出来，FixedHostKey 就对不上。
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
		Timeout:           3 * time.Second,
	})
	if err != nil {
		t.Fatalf("direct ssh dial: %v", err)
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		t.Fatalf("direct sftp client: %v", err)
	}
	// 旁路连接的认证不计入被测适配器的认证次数。
	srv.authAttempts.Store(0)
	return sc
}

func (srv *fakeSFTP) write(t *testing.T, remotePath string, content []byte) {
	t.Helper()
	if err := srv.direct.MkdirAll(path.Dir(remotePath)); err != nil {
		t.Fatalf("mkdirall %s: %v", path.Dir(remotePath), err)
	}
	f, err := srv.direct.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatalf("create %s: %v", remotePath, err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatalf("write %s: %v", remotePath, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", remotePath, err)
	}
}

func (srv *fakeSFTP) read(t *testing.T, remotePath string) ([]byte, bool) {
	t.Helper()
	f, err := srv.direct.Open(remotePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false
		}
		t.Fatalf("open %s: %v", remotePath, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read %s: %v", remotePath, err)
	}
	return data, true
}

func (srv *fakeSFTP) list(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := srv.direct.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatalf("readdir %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func (srv *fakeSFTP) corruptReadsOf(remotePath string) {
	srv.mu.Lock()
	srv.corrupt[remotePath] = true
	srv.mu.Unlock()
}

func cbNewSFTPStore(t *testing.T, srv *fakeSFTP, pin string, mutate func(*SFTPTarget)) *SFTPRemoteStore {
	t.Helper()
	target := SFTPTarget{
		TargetID:         "target-a",
		SiteID:           "sitea",
		Host:             "127.0.0.1",
		Port:             srv.port,
		Username:         srv.user,
		Password:         srv.pass,
		HostKeySHA256:    pin,
		ConnectTimeout:   3 * time.Second,
		OperationTimeout: 10 * time.Second,
	}
	if mutate != nil {
		mutate(&target)
	}
	store, err := NewSFTPRemoteStore(target)
	if err != nil {
		t.Fatalf("NewSFTPRemoteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func cbWaitConnsClosed(t *testing.T, srv *fakeSFTP, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// 旁路连接自己占 1 条。
		if srv.openConns.Load() <= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("适配器的连接没有关闭：仍有 %d 条（含旁路 1 条）", srv.openConns.Load())
}

// pin 的格式必须与 OpenSSH 工具链一致：ssh-keygen -lf 打印 "SHA256:<base64>"，我们存
// 小写 hex，两者是同一段字节的两种编码。这条不成立，运营就无法从 ssh-keyscan 得到可用的 pin。
func TestContentBackupSFTPHostKeyPinMatchesOpenSSHFingerprint(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	openssh := ssh.FingerprintSHA256(signer.PublicKey())
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(openssh, "SHA256:"))
	if err != nil {
		t.Fatalf("decode openssh fingerprint %q: %v", openssh, err)
	}
	if got := HostKeySHA256Hex(signer.PublicKey()); got != hex.EncodeToString(raw) {
		t.Fatalf("HostKeySHA256Hex = %s, openssh form decodes to %s", got, hex.EncodeToString(raw))
	}
}

func TestContentBackupSFTPPutVerifiedRoundTrip(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	content := cbTestContent(64 * 1024)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	if err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
		t.Fatalf("PutVerified: %v", err)
	}
	got, ok := srv.read(t, job.RemotePath)
	if !ok || !bytes.Equal(got, content) {
		t.Fatalf("final file missing or differs (exists=%v, %d bytes)", ok, len(got))
	}
	for _, dir := range []string{path.Dir(job.RemotePath), path.Dir(cbTempPath(t, job))} {
		for _, name := range srv.list(t, dir) {
			if strings.HasSuffix(name, sftpTempSuffix) {
				t.Fatalf("temp file left behind after success: %s/%s", dir, name)
			}
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close after put: %v", err)
	}
	cbWaitConnsClosed(t, srv, 3*time.Second)

	body, err := store.Open(context.Background(), job)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	back, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("second Close must be a no-op: %v", err)
	}
	if !bytes.Equal(back, content) {
		t.Fatal("Open returned different bytes")
	}
	cbWaitConnsClosed(t, srv, 3*time.Second)

	if err := store.Remove(context.Background(), job); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := srv.read(t, job.RemotePath); ok {
		t.Fatal("Remove left the file in place")
	}
	_, err = store.Open(context.Background(), job)
	if !errors.Is(err, ErrFTPSMissing) || !errors.Is(err, ErrSFTPMissing) {
		t.Fatalf("Open of a removed file must be the missing class for read.go, got %v", err)
	}
	if info := ClassifyUploadError(err); info.Code != UploadCodeRemoteMissing || !info.Retryable {
		t.Fatalf("missing must stay retryable remote_missing, got %+v", info)
	}
}

// 错 pin 必须在认证之前就断：口令一个字节都不能发给一台身份未证实的机器。
func TestContentBackupSFTPWrongHostKeyPinRejectsBeforeAuth(t *testing.T) {
	srv := cbStartSFTP(t)
	other := cbStartSFTP(t) // 另一台机器的真实 host key，pin 格式合法但不属于 srv
	store := cbNewSFTPStore(t, srv, other.pin, nil)
	content := cbTestContent(2048)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSCertificate) || !errors.Is(err, ErrSFTPHostKey) {
		t.Fatalf("wrong pin must be the certificate/host-key class, got %v", err)
	}
	if !strings.Contains(err.Error(), "ssh-ed25519") || !strings.Contains(err.Error(), "SHA256:") {
		t.Fatalf("error must tell the operator the observed key type and OpenSSH fingerprint: %v", err)
	}
	if got := srv.authAttempts.Load(); got != 0 {
		t.Fatalf("password was sent to an unverified host: %d auth attempts", got)
	}
	if _, ok := srv.read(t, job.RemotePath); ok {
		t.Fatal("nothing may be written after a pin failure")
	}
	info := ClassifyUploadError(err)
	if info.Code != UploadCodeCertificate || !info.PauseTarget || info.Retryable {
		t.Fatalf("pin failure must pause the target as a terminal failure, got %+v", info)
	}
	cbWaitConnsClosed(t, srv, 3*time.Second)
}

// 服务器有 ed25519 + ecdsa 两把密钥时必须协商出 ed25519：x/crypto 的默认偏好是 ECDSA 在前，
// 若沿用默认，运营按提示 `ssh-keyscan -t ed25519` 取到的 pin 永远对不上服务器实际拿出来的密钥。
func TestContentBackupSFTPNegotiatesEd25519BeforeECDSA(t *testing.T) {
	srv := cbStartSFTP(t)
	content := cbTestContent(256)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	// ed25519 pin → 成功（其它用例都依赖这一点）
	if err := cbNewSFTPStore(t, srv, srv.pin, nil).PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
		t.Fatalf("ed25519 pin must match the negotiated key: %v", err)
	}
	// ecdsa pin → 失败，且错误里说明服务器实际拿出的是 ssh-ed25519，让运营知道该 pin 哪一把
	err := cbNewSFTPStore(t, srv, srv.ecdsaPin, nil).PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrSFTPHostKey) {
		t.Fatalf("pinning the server's ecdsa key must fail because ed25519 is negotiated, got %v", err)
	}
	if !strings.Contains(err.Error(), "offered ssh-ed25519 key") {
		t.Fatalf("error must name the negotiated key type: %v", err)
	}
}

func TestContentBackupSFTPAuthFailurePausesTarget(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, func(tg *SFTPTarget) { tg.Password = "nope" })
	content := cbTestContent(2048)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSAuth) || !errors.Is(err, ErrSFTPAuth) {
		t.Fatalf("rejected credentials must be the auth class, got %v", err)
	}
	if srv.authAttempts.Load() == 0 {
		t.Fatal("the server never saw an auth attempt, so this did not test auth")
	}
	info := ClassifyUploadError(err)
	if info.Code != UploadCodeAuth || !info.PauseTarget || info.Retryable {
		t.Fatalf("auth failure must be terminal + pause target, got %+v", info)
	}
	if _, ok := srv.read(t, job.RemotePath); ok {
		t.Fatal("nothing may be written after an auth failure")
	}
	cbWaitConnsClosed(t, srv, 3*time.Second)
}

// 写完读回摘要不符 → 不能改名成正式文件，临时文件也要清掉；这类是可重试的远端问题。
func TestContentBackupSFTPReadBackMismatchNeverRenames(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	content := cbTestContent(4096)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
	tempPath := cbTempPath(t, job) + "/" + job.JobID + "." + lease.Token + sftpTempSuffix
	dir := path.Dir(tempPath)
	srv.corruptReadsOf(tempPath)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSHashMismatch) || !errors.Is(err, ErrSFTPHashMismatch) {
		t.Fatalf("corrupted read-back must be the hash-mismatch class, got %v", err)
	}
	if _, ok := srv.read(t, job.RemotePath); ok {
		t.Fatal("a file whose read-back did not verify must never be renamed to the final name")
	}
	for _, name := range srv.list(t, dir) {
		if strings.HasSuffix(name, sftpTempSuffix) {
			t.Fatalf("temp file must be discarded after a failed verification: %s", name)
		}
	}
	if info := ClassifyUploadError(err); info.Code != UploadCodeRemoteHashMismatch || !info.Retryable {
		t.Fatalf("read-back mismatch stays retryable, got %+v", info)
	}
}

func TestContentBackupSFTPFinalWithSameContentIsIdempotent(t *testing.T) {
	// 最终文件已经在、DB 却没记上，只可能发生在重试上（上一轮改名成功后提交失败）。
	t.Run("retry detects the finished upload without re-sending", func(t *testing.T) {
		srv := cbStartSFTP(t)
		store := cbNewSFTPStore(t, srv, srv.pin, nil)
		content := cbTestContent(3000)
		job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
		lease.Generation = 2
		srv.write(t, job.RemotePath, content)

		// src 永远不会被读取：远端已经有一模一样的内容，不需要再传一次。
		untouched := &ftpsTrackedReader{r: bytes.NewReader(content)}
		if err := store.PutVerified(context.Background(), job, lease, untouched); err != nil {
			t.Fatalf("same content under the final name must be an idempotent success: %v", err)
		}
		if untouched.n != 0 {
			t.Fatalf("PutVerified re-uploaded %d bytes although the final file already matched", untouched.n)
		}
	})
	// 首次认领不做预检（省一个往返）；万一真碰上同名同内容，改名失败后仍要收敛到成功且不留临时文件。
	t.Run("first attempt still converges to success", func(t *testing.T) {
		srv := cbStartSFTP(t)
		store := cbNewSFTPStore(t, srv, srv.pin, nil)
		content := cbTestContent(3000)
		job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
		srv.write(t, job.RemotePath, content)

		if err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
			t.Fatalf("same content under the final name must still end as success: %v", err)
		}
		if got, ok := srv.read(t, job.RemotePath); !ok || !bytes.Equal(got, content) {
			t.Fatal("the final file must keep its exact content")
		}
		for _, name := range srv.list(t, cbTempPath(t, job)) {
			if strings.HasSuffix(name, sftpTempSuffix) {
				t.Fatalf("the refused rename must not leave a temp behind: %s", name)
			}
		}
	})
}

func TestContentBackupSFTPFinalWithDifferentContentIsConflict(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	content := cbTestContent(3000)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
	foreign := []byte("someone else's archive")
	srv.write(t, job.RemotePath, foreign)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSConflict) || !errors.Is(err, ErrSFTPConflict) {
		t.Fatalf("different content under the final name must be remote_conflict, got %v", err)
	}
	got, _ := srv.read(t, job.RemotePath)
	if !bytes.Equal(got, foreign) {
		t.Fatal("the existing final file must never be overwritten")
	}
	if info := ClassifyUploadError(err); info.Code != UploadCodeRemoteConflict || info.Retryable {
		t.Fatalf("conflict is terminal, got %+v", info)
	}
}

// 旧租约留下的同 job 临时文件要被清掉；别的 job 的临时文件一个都不能碰。
func TestContentBackupSFTPSweepsOnlyOwnStaleTemps(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	content := cbTestContent(1024)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
	dir := cbTempPath(t, job)
	stale := dir + "/" + job.JobID + ".oldleasetoken0000" + sftpTempSuffix
	otherJob := dir + "/00000000-0000-4000-8000-00000000abcd.sometoken00" + sftpTempSuffix
	archiveStale := path.Dir(job.RemotePath) + "/" + job.JobID + ".oldleasetoken0000" + sftpTempSuffix
	srv.write(t, stale, []byte("stale"))
	srv.write(t, otherJob, []byte("other"))
	srv.write(t, archiveStale, []byte("archive"))

	// 首次认领（generation 1）不可能有上一次租约的临时文件，所以根本不列目录。
	if err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
		t.Fatalf("first PutVerified: %v", err)
	}
	if got, ok := srv.read(t, stale); !ok || string(got) != "stale" {
		t.Fatal("a first attempt must not list the incoming shard, so the stale temp stays")
	}

	// 重试才清扫：清扫发生在"最终文件是否已存在"之前，所以这一轮虽然会认出
	// 文件已经传好并直接返回，临时文件也必须被删掉。
	lease.Generation = 2
	if err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
		t.Fatalf("retry PutVerified: %v", err)
	}
	if _, ok := srv.read(t, stale); ok {
		t.Fatal("stale temp of the same job must be swept")
	}
	if got, ok := srv.read(t, otherJob); !ok || string(got) != "other" {
		t.Fatal("another job's temp must not be touched")
	}
	if got, ok := srv.read(t, archiveStale); !ok || string(got) != "archive" {
		t.Fatal("a leftover temp in the archive directory must stay; that directory is no longer listed")
	}
}

func cbTempPath(t *testing.T, job model.ContentBackupJob) string {
	t.Helper()
	dir, err := contentbackup.IncomingDir(job.SiteID, job.JobID)
	if err != nil {
		t.Fatalf("incoming dir: %v", err)
	}
	return dir
}

func TestContentBackupSFTPCancelClosesConnection(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	content := cbTestContent(8192)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	ctx, cancel := context.WithCancel(context.Background())
	// 读到第一块就取消：上传卡在半路，取消必须让它返回并关连接，而不是等 OperationTimeout。
	src := &cancelAfterFirstRead{r: bytes.NewReader(content), cancel: cancel, block: make(chan struct{})}
	start := time.Now()
	err := store.PutVerified(ctx, job, lease, src)
	if err == nil {
		t.Fatal("a cancelled upload must not report success")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must surface context.Canceled for T05, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancel took %s, the blocked transfer was not interrupted", elapsed)
	}
	if info := ClassifyUploadError(err); info.Code != UploadCodeCancelled {
		t.Fatalf("cancel classifies as cancelled, got %+v", info)
	}
	if _, ok := srv.read(t, job.RemotePath); ok {
		t.Fatal("a cancelled upload must never produce a final file")
	}
	cbWaitConnsClosed(t, srv, 3*time.Second)
}

type cancelAfterFirstRead struct {
	r      io.Reader
	cancel context.CancelFunc
	once   sync.Once
	block  chan struct{}
}

func (c *cancelAfterFirstRead) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.once.Do(func() {
		c.cancel()
		return
	})
	if n == 0 && err == nil {
		<-c.block
	}
	return n, err
}

func TestContentBackupSFTPConstructionErrorsAreTargetConfig(t *testing.T) {
	base := SFTPTarget{TargetID: "t", SiteID: "sitea", Host: "h", Port: 22, Username: "u", Password: "p", HostKeySHA256: strings.Repeat("ab", 32)}
	cases := []struct {
		name   string
		mutate func(*SFTPTarget)
		want   error
		code   string
	}{
		{"missing target id", func(t *SFTPTarget) { t.TargetID = "" }, ErrFTPSConfig, UploadCodeTargetConfig},
		{"missing host", func(t *SFTPTarget) { t.Host = "" }, ErrFTPSConfig, UploadCodeTargetConfig},
		{"port out of range", func(t *SFTPTarget) { t.Port = 70000 }, ErrFTPSConfig, UploadCodeTargetConfig},
		{"missing username", func(t *SFTPTarget) { t.Username = "" }, ErrFTPSConfig, UploadCodeTargetConfig},
		{"bad site label", func(t *SFTPTarget) { t.SiteID = "Bad Site" }, ErrFTPSConfig, UploadCodeTargetConfig},
		{"short pin", func(t *SFTPTarget) { t.HostKeySHA256 = "abc" }, ErrFTPSInvalidPin, UploadCodeInvalidPin},
		{"uppercase pin", func(t *SFTPTarget) { t.HostKeySHA256 = strings.Repeat("AB", 32) }, ErrFTPSInvalidPin, UploadCodeInvalidPin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := base
			tc.mutate(&target)
			_, err := NewSFTPRemoteStore(target)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if info := ClassifyUploadError(err); info.Code != tc.code || !info.PauseTarget || info.Retryable {
				t.Fatalf("construction errors must pause the target without retries, got %+v", info)
			}
		})
	}
	if _, err := NewSFTPRemoteStore(base); err != nil {
		t.Fatalf("baseline target must construct: %v", err)
	}
}

func TestContentBackupSFTPCheckJobRejectsForeignOrUnsafeJobs(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	content := cbTestContent(128)
	good, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	cases := []struct {
		name   string
		mutate func(job *model.ContentBackupJob, lease *model.ContentBackupLease)
	}{
		{"other target", func(j *model.ContentBackupJob, _ *model.ContentBackupLease) { j.TargetID = "target-b" }},
		{"other site prefix", func(j *model.ContentBackupJob, _ *model.ContentBackupLease) {
			j.RemotePath = strings.Replace(j.RemotePath, "/sitea/", "/siteb/", 1)
		}},
		{"path traversal", func(j *model.ContentBackupJob, _ *model.ContentBackupLease) {
			j.RemotePath = "/sitea/../etc/" + j.JobID + ".json.gz"
		}},
		{"base not job id", func(j *model.ContentBackupJob, _ *model.ContentBackupLease) {
			j.RemotePath = path.Dir(j.RemotePath) + "/other.json.gz"
		}},
		{"unsafe lease token", func(_ *model.ContentBackupJob, l *model.ContentBackupLease) { l.Token = "../../x" }},
		{"lease for another job", func(_ *model.ContentBackupJob, l *model.ContentBackupLease) {
			l.JobID = "00000000-0000-4000-8000-000000000001"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job, l := good, lease
			tc.mutate(&job, &l)
			err := store.PutVerified(context.Background(), job, l, bytes.NewReader(content))
			if !errors.Is(err, ErrFTPSInvalidJob) {
				t.Fatalf("want invalid job, got %v", err)
			}
			if info := ClassifyUploadError(err); info.Code != UploadCodeInvalidJob || info.Retryable {
				t.Fatalf("invalid job is terminal, got %+v", info)
			}
		})
	}
	if srv.authAttempts.Load() != 0 {
		t.Fatal("job validation must reject before any connection is made")
	}
}

// 每个 SFTP 哨兵都必须落到与 FTPS 对应哨兵同一行状态机：分类器只认 ErrFTPS*，
// 若某个 sftp 错误漏映射，它会掉进 unknown → 可重试，配置错误就会白跑 16 次。
func TestContentBackupSFTPSentinelsClassifyLikeFTPS(t *testing.T) {
	pairs := []struct{ sftp, ftps error }{
		{ErrSFTPConfig, ErrFTPSConfig},
		{ErrSFTPInvalidJob, ErrFTPSInvalidJob},
		{ErrSFTPInvalidPin, ErrFTPSInvalidPin},
		{ErrSFTPHostKey, ErrFTPSCertificate},
		{ErrSFTPAuth, ErrFTPSAuth},
		{ErrSFTPPermission, ErrFTPSPermission},
		{ErrSFTPConflict, ErrFTPSConflict},
		{ErrSFTPHashMismatch, ErrFTPSHashMismatch},
		{ErrSFTPLocalSource, ErrFTPSLocalSource},
		{ErrSFTPMissing, ErrFTPSMissing},
		{ErrSFTPTimeout, ErrFTPSTimeout},
		{ErrSFTPNetwork, ErrFTPSNetwork},
		{ErrSFTPUnsupported, ErrFTPSUnsupported},
	}
	for _, pair := range pairs {
		if !errors.Is(pair.sftp, pair.ftps) {
			t.Fatalf("%v must satisfy errors.Is against %v", pair.sftp, pair.ftps)
		}
		if strings.Contains(pair.sftp.Error(), "ftps") {
			t.Fatalf("sftp sentinel message must name its own protocol: %v", pair.sftp)
		}
		got, want := ClassifyUploadError(pair.sftp), ClassifyUploadError(pair.ftps)
		if got != want {
			t.Fatalf("%v classifies as %+v, ftps counterpart %+v", pair.sftp, got, want)
		}
	}
	// 反向：一个 sftp 哨兵不能误伤别的 FTPS 类别。
	if errors.Is(ErrSFTPAuth, ErrFTPSCertificate) || errors.Is(ErrSFTPMissing, ErrFTPSConflict) {
		t.Fatal("sftp sentinels must map one-to-one")
	}
	// 关键反例：远端 "no such file" 在 pkg/sftp 里会被规范化成 os.ErrNotExist，而分类器把裸的
	// os.ErrNotExist 当成本地备份丢失（不可恢复）。适配器返回的错误绝不能透出它。
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	job, _ := cbTestJobAndLease(t, cbTestContent(16), cbTestLeaseToken)
	_, err := store.Open(context.Background(), job)
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remote missing must not leak os.ErrNotExist (would classify as local_missing): %v", err)
	}
	if info := ClassifyUploadError(err); info.Code != UploadCodeRemoteMissing {
		t.Fatalf("remote missing classifies as %+v", info)
	}
}

// 真实目标 5.45.76.50 的 SFTP 没有 chroot：登录后 cwd 是 /raid/backup/b_459494，而 "/" 是真根、
// 不可写（2026-09-18 只读探测）。逻辑 remote_path 仍是 "/{site}/..."（DB 里不可变），物理路径
// 必须挂在账号登录目录（默认）或显式 base dir 之下；否则第一次 mkdir /sitea 就 EACCES。
func TestContentBackupSFTPAnchorsLogicalTreeUnderLoginDirectory(t *testing.T) {
	home := "/raid/backup/b_459494"
	srv := cbStartSFTPAt(t, home)
	srv.write(t, home+"/.bashrc", []byte("# pre-existing account file"))
	store := cbNewSFTPStore(t, srv, srv.pin, nil) // BaseDir 留空 → 用服务器报告的登录目录
	content := cbTestContent(1500)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	if err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
		t.Fatalf("PutVerified under a non-chrooted account: %v", err)
	}
	if !strings.HasPrefix(job.RemotePath, "/sitea/") {
		t.Fatalf("logical remote_path must stay site-rooted, got %s", job.RemotePath)
	}
	if got, ok := srv.read(t, home+job.RemotePath); !ok || !bytes.Equal(got, content) {
		t.Fatalf("file must land at login dir + logical path (%s), exists=%v", home+job.RemotePath, ok)
	}
	if _, ok := srv.read(t, job.RemotePath); ok {
		t.Fatal("nothing may be written at the real root")
	}
	// 读回与删除也必须走同一映射，否则上传成功后预览永远 remote_missing。
	body, err := store.Open(context.Background(), job)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	back, _ := io.ReadAll(body)
	_ = body.Close()
	if !bytes.Equal(back, content) {
		t.Fatal("Open must read from the anchored physical path")
	}
	if err := store.Remove(context.Background(), job); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := srv.read(t, home+job.RemotePath); ok {
		t.Fatal("Remove must delete the anchored physical file")
	}
	if got, ok := srv.read(t, home+"/.bashrc"); !ok || string(got) != "# pre-existing account file" {
		t.Fatal("unrelated account files must be untouched")
	}

	// 显式 base dir 优先于登录目录。
	explicit := cbNewSFTPStore(t, srv, srv.pin, func(tg *SFTPTarget) { tg.BaseDir = home + "/archive" })
	srv.write(t, home+"/archive/.keep", nil)
	if err := explicit.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
		t.Fatalf("PutVerified with explicit base dir: %v", err)
	}
	if _, ok := srv.read(t, home+"/archive"+job.RemotePath); !ok {
		t.Fatal("explicit base dir must anchor the tree")
	}
	// 非法 base dir 是配置错误，落 target_config 终态而不是白跑退避。
	_, err = NewSFTPRemoteStore(SFTPTarget{TargetID: "t", SiteID: "sitea", Host: "h", Port: 22, Username: "u", HostKeySHA256: srv.pin, BaseDir: "relative/dir"})
	if !errors.Is(err, ErrFTPSConfig) {
		t.Fatalf("relative base dir must be a config error, got %v", err)
	}
}

// 生产工厂按配置快照选协议：remote_protocol=sftp 时必须造出 SFTP 适配器并真的能连上
// 假服务器；留空/ftps 走原 FTPS 适配器。凭据仍从 daemon 自己的环境变量读。
func TestContentBackupSFTPFactorySelectsProtocol(t *testing.T) {
	srv := cbStartSFTP(t)
	cfg := contentbackup.DefaultConfig()
	cfg.TargetID = "target-a"
	cfg.RemoteUsername = srv.user
	cfg.RemotePassword = srv.pass
	cfg.RemoteProtocol = contentbackup.RemoteProtocolSFTP
	cfg.SFTPHost = "127.0.0.1"
	cfg.SFTPPort = srv.port
	cfg.SFTPHostKeySHA256 = srv.pin
	factory := newRemoteFactory("sitea", func() contentbackup.Config { return cfg })

	remote, err := factory(cfg.TargetID)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if _, ok := remote.(*SFTPRemoteStore); !ok {
		t.Fatalf("remote_protocol=sftp must build an SFTP store, got %T", remote)
	}
	content := cbTestContent(512)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
	if err := remote.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
		t.Fatalf("factory-built store must upload against the fake server: %v", err)
	}
	if got, ok := srv.read(t, job.RemotePath); !ok || !bytes.Equal(got, content) {
		t.Fatal("upload through the factory-built store did not land")
	}

	legacy := cfg
	legacy.RemoteProtocol = ""
	legacy.FTPSHost = "127.0.0.1"
	legacy.CertSHA256 = strings.Repeat("ab", 32)
	remote, err = newRemoteFactory("sitea", func() contentbackup.Config { return legacy })(cfg.TargetID)
	if err != nil {
		t.Fatalf("legacy factory: %v", err)
	}
	if _, ok := remote.(*FTPSRemoteStore); !ok {
		t.Fatalf("empty remote_protocol must keep building the FTPS store, got %T", remote)
	}
}

// 「测试连接」六阶段对着 SFTP 适配器真跑：合成任务满足 checkJob 约束，写/读/哈希/改名/删除全过。
func TestContentBackupSFTPProbeSixStages(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	reader := t16Reader(t, func(string) (RemoteStore, error) { return store, nil }, nil)

	result, err := reader.Probe(context.Background(), t16Target, "127.0.0.1")
	if err != nil {
		t.Fatalf("Probe over sftp: %v (stages: %+v)", err, result.Stages)
	}
	for _, name := range []string{"connect", "write", "read", "hash", "rename", "delete"} {
		if stage := t16Stage(t, result, name); !stage.OK {
			t.Fatalf("stage %q failed: %+v", name, stage)
		}
	}
	if names := srv.list(t, "/"+t16Site+"/probe"); len(names) == 0 {
		t.Fatal("probe directory should exist after the probe ran")
	} else {
		for _, day := range names {
			for _, left := range srv.list(t, "/"+t16Site+"/probe/"+day) {
				t.Fatalf("probe must delete its own synthetic file, found %s", left)
			}
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close after probe: %v", err)
	}
	cbWaitConnsClosed(t, srv, 3*time.Second)
}

func TestContentBackupSFTPPutVerifiedReusesSession(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	before := srv.accepts.Load()

	first, firstLease := cbTestJobAndLease(t, cbTestContent(64), cbTestLeaseToken)
	if err := store.PutVerified(context.Background(), first, firstLease, bytes.NewReader(cbTestContent(64))); err != nil {
		t.Fatalf("first put: %v", err)
	}
	afterFirst := srv.accepts.Load()
	if afterFirst != before+1 {
		t.Fatalf("first put accepts = %d, want %d", afterFirst, before+1)
	}

	second, secondLease := cbTestJobAndLease(t, cbTestContent(64), "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err := store.PutVerified(context.Background(), second, secondLease, bytes.NewReader(cbTestContent(64))); err != nil {
		t.Fatalf("second put: %v", err)
	}
	if got := srv.accepts.Load(); got != afterFirst {
		t.Fatalf("second put opened a new SSH session (accepts %d, want %d)", got, afterFirst)
	}

	ctx, cancel := context.WithCancel(context.Background())
	third, thirdLease := cbTestJobAndLease(t, cbTestContent(64), "cccccccccccccccccccccccccccccccc")
	if err := store.PutVerified(ctx, third, thirdLease, bytes.NewReader(cbTestContent(64))); err != nil {
		t.Fatalf("third put: %v", err)
	}
	cancel()
	fourth, fourthLease := cbTestJobAndLease(t, cbTestContent(64), "dddddddddddddddddddddddddddddddd")
	if err := store.PutVerified(context.Background(), fourth, fourthLease, bytes.NewReader(cbTestContent(64))); err != nil {
		t.Fatalf("put after caller cancel: %v", err)
	}
	if got := srv.accepts.Load(); got != afterFirst {
		t.Fatalf("caller cancel after a successful put must not kill the pooled session (accepts %d, want %d)", got, afterFirst)
	}
}

// 孤儿临时文件回收只拿列表里能解析出 job+token 的临时文件：外来文件、归档、子目录、别的分片一个都不能交出去。
func TestContentBackupSFTPListIncomingKeepsOnlyOwnTempsOfTheShard(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	jobA, jobB, jobC := cbJobInShard("ab"), cbJobInShard("ab"), cbJobInShard("cd")
	tokenOld := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shardDir := path.Dir(cbIncomingTempPath(t, jobA, cbTestLeaseToken))
	want := []string{jobA + "." + cbTestLeaseToken, jobA + "." + tokenOld, jobB + "." + cbTestLeaseToken}
	written := time.Now().Truncate(time.Second) // SFTP mtimes carry whole seconds
	for _, key := range want {
		srv.write(t, shardDir+"/"+key+sftpTempSuffix, []byte("temp"))
	}
	for _, name := range cbForeignIncomingNames(jobA) {
		srv.write(t, shardDir+"/"+name, []byte("foreign"))
	}
	if err := srv.direct.MkdirAll(cbIncomingTempPath(t, jobB, tokenOld)); err != nil {
		t.Fatalf("mkdir a directory named like a temp: %v", err)
	}
	srv.write(t, cbIncomingTempPath(t, jobC, cbTestLeaseToken), []byte("other shard"))
	archive := "/sitea/2026-09-15/ab/" + jobA + contentbackup.RemoteFileExtension
	srv.write(t, archive, []byte("archive"))
	srv.write(t, path.Dir(archive)+"/"+jobA+".deadbeefdeadbeef"+sftpTempSuffix, []byte("old layout temp"))
	before := srv.accepts.Load()

	temps, err := store.ListIncoming(context.Background(), "ab")
	if err != nil {
		t.Fatalf("ListIncoming: %v", err)
	}
	got := cbTempsByKey(t, temps)
	if keys := cbSortedKeys(got); len(keys) != len(want) {
		t.Fatalf("want exactly the %d temps of shard ab, got %v", len(want), keys)
	}
	for _, key := range want {
		temp, ok := got[key]
		if !ok {
			t.Fatalf("temp %s missing from %v", key, cbSortedKeys(got))
		}
		if temp.ModTime.Before(written.Add(-time.Second)) || temp.ModTime.After(time.Now().Add(time.Second)) {
			t.Fatalf("temp %s ModTime = %v, want the server's write time (~%v)", key, temp.ModTime, written)
		}
	}

	other, err := store.ListIncoming(context.Background(), "cd")
	if err != nil {
		t.Fatalf("ListIncoming cd: %v", err)
	}
	if keys := cbSortedKeys(cbTempsByKey(t, other)); len(keys) != 1 || keys[0] != jobC+"."+cbTestLeaseToken {
		t.Fatalf("shard cd must list only its own temp, got %v", keys)
	}
	if got := srv.accepts.Load(); got != before+1 {
		t.Fatalf("listings must reuse one pooled session (accepts %d, want %d)", got, before+1)
	}
}

type sftpStubFileInfo struct {
	os.FileInfo
	mod time.Time
}

func (f sftpStubFileInfo) ModTime() time.Time { return f.mod }

// A server that sends no mtime makes pkg/sftp report the Unix epoch; that must surface as
// "age unknown" (zero), or the reaper's age gate would read it as 56 years old.
func TestContentBackupSFTPEntryModTimeTreatsEpochAsUnknown(t *testing.T) {
	if got := sftpEntryModTime(sftpStubFileInfo{mod: time.Unix(0, 0)}); !got.IsZero() {
		t.Fatalf("epoch mtime = %v, want the zero time", got)
	}
	written := time.Unix(1_790_000_000, 0)
	if got := sftpEntryModTime(sftpStubFileInfo{mod: written}); !got.Equal(written) {
		t.Fatalf("real mtime = %v, want %v", got, written)
	}
}

func TestContentBackupSFTPListIncomingMissingShardIsEmpty(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	before := srv.accepts.Load()

	// 从没上传过：连 /sitea/.incoming 都不存在。
	temps, err := store.ListIncoming(context.Background(), "00")
	if err != nil || len(temps) != 0 {
		t.Fatalf("a never-created shard must list as empty, got %v, %v", temps, err)
	}
	srv.write(t, cbIncomingTempPath(t, cbJobInShard("ab"), cbTestLeaseToken), []byte("sibling shard"))
	temps, err = store.ListIncoming(context.Background(), "ef")
	if err != nil || len(temps) != 0 {
		t.Fatalf("a shard missing next to an existing one must list as empty, got %v, %v", temps, err)
	}
	if got := srv.accepts.Load(); got != before+1 {
		t.Fatalf("a missing shard is an answer, not a broken session (accepts %d, want %d)", got, before+1)
	}
}

// 列目录失败的会话必须丢掉而不是回池；错误要归到远端类别，不能透出 os.ErrNotExist。
func TestContentBackupSFTPListIncomingFailureDiscardsSession(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	shardDir, err := contentbackup.IncomingShardDir("sitea", "ab")
	if err != nil {
		t.Fatalf("shard dir: %v", err)
	}
	srv.write(t, shardDir, []byte("a file where the shard directory belongs"))
	before := srv.accepts.Load()

	_, err = store.ListIncoming(context.Background(), "ab")
	if !errors.Is(err, ErrFTPSPermission) || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an unreadable shard must be a remote refusal, got %v", err)
	}
	if _, err := store.ListIncoming(context.Background(), "cd"); err != nil {
		t.Fatalf("ListIncoming after a failure: %v", err)
	}
	if got := srv.accepts.Load(); got != before+2 {
		t.Fatalf("a failed listing must not return its session to the pool (accepts %d, want %d)", got, before+2)
	}
}

func TestContentBackupSFTPRemoveIncomingDeletesOnlyThatTemp(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	jobID, siblingJob := cbJobInShard("ab"), cbJobInShard("ab")
	target := cbIncomingTempPath(t, jobID, cbTestLeaseToken)
	keep := []string{
		cbIncomingTempPath(t, jobID, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		cbIncomingTempPath(t, siblingJob, cbTestLeaseToken),
		"/sitea/2026-09-15/ab/" + jobID + contentbackup.RemoteFileExtension,
	}
	srv.write(t, target, []byte("dead temp"))
	for _, p := range keep {
		srv.write(t, p, []byte("keep"))
	}
	before := srv.accepts.Load()

	if err := store.RemoveIncoming(context.Background(), IncomingTemp{JobID: jobID, Token: cbTestLeaseToken}); err != nil {
		t.Fatalf("RemoveIncoming: %v", err)
	}
	if _, ok := srv.read(t, target); ok {
		t.Fatal("the temp must be deleted")
	}
	for _, p := range keep {
		if got, ok := srv.read(t, p); !ok || string(got) != "keep" {
			t.Fatalf("%s must not be touched", p)
		}
	}
	if err := store.RemoveIncoming(context.Background(), IncomingTemp{JobID: jobID, Token: cbTestLeaseToken}); err != nil {
		t.Fatalf("removing an already-deleted temp must not be an error, got %v", err)
	}
	if err := store.RemoveIncoming(context.Background(), IncomingTemp{JobID: cbJobInShard("ef"), Token: cbTestLeaseToken}); err != nil {
		t.Fatalf("removing a temp from a never-created shard must not be an error, got %v", err)
	}
	if got := srv.accepts.Load(); got != before+1 {
		t.Fatalf("removes must reuse one pooled session (accepts %d, want %d)", got, before+1)
	}
}

func TestContentBackupSFTPIncomingRejectsInvalidInputBeforeConnecting(t *testing.T) {
	srv := cbStartSFTP(t)
	store := cbNewSFTPStore(t, srv, srv.pin, nil)
	before := srv.accepts.Load()
	for _, shard := range cbInvalidShards {
		if temps, err := store.ListIncoming(context.Background(), shard); !errors.Is(err, ErrFTPSInvalidJob) || temps != nil {
			t.Fatalf("shard %q: want the invalid-job class, got %v, %v", shard, temps, err)
		}
	}
	for _, temp := range cbInvalidIncomingTemps() {
		if err := store.RemoveIncoming(context.Background(), temp); !errors.Is(err, ErrSFTPInvalidJob) {
			t.Fatalf("temp %+v: want ErrSFTPInvalidJob, got %v", temp, err)
		}
	}
	if got := srv.accepts.Load(); got != before || srv.authAttempts.Load() != 0 {
		t.Fatalf("invalid input must be rejected before any connection (accepts %d→%d, auth %d)", before, got, srv.authAttempts.Load())
	}
}

// 未 chroot 的账号：列目录和删除必须走与上传同一套物理路径映射，真根下的同名文件不能碰。
func TestContentBackupSFTPIncomingAnchorsUnderBaseDir(t *testing.T) {
	home := "/raid/backup/b_459494"
	srv := cbStartSFTPAt(t, home)
	jobID := cbJobInShard("ab")
	logical := cbIncomingTempPath(t, jobID, cbTestLeaseToken)
	// 真根下一个同名、一个别的 token：列错目录会多列出后者，删错目录会删掉前者。
	rootOnly := cbIncomingTempPath(t, jobID, "rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr")
	srv.write(t, logical, []byte("same name at the real root"))
	srv.write(t, rootOnly, []byte("only at the real root"))

	cases := []struct {
		name   string
		base   string
		mutate func(*SFTPTarget)
	}{
		{"login directory", home, nil},
		{"explicit base dir", home + "/archive", func(tg *SFTPTarget) { tg.BaseDir = home + "/archive" }},
	}
	for _, tc := range cases {
		srv.write(t, tc.base+logical, []byte("anchored temp"))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := cbNewSFTPStore(t, srv, srv.pin, tc.mutate)
			temps, err := store.ListIncoming(context.Background(), "ab")
			if err != nil {
				t.Fatalf("ListIncoming: %v", err)
			}
			if len(temps) != 1 || temps[0].JobID != jobID || temps[0].Token != cbTestLeaseToken {
				t.Fatalf("want the one temp under %s, got %+v", tc.base, temps)
			}
			if err := store.RemoveIncoming(context.Background(), temps[0]); err != nil {
				t.Fatalf("RemoveIncoming: %v", err)
			}
			if _, ok := srv.read(t, tc.base+logical); ok {
				t.Fatalf("the temp under %s must be deleted", tc.base)
			}
		})
	}
	for _, p := range []string{logical, rootOnly} {
		if _, ok := srv.read(t, p); !ok {
			t.Fatalf("%s at the real root must be untouched", p)
		}
	}
}
