package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/OpenBazaar/openbazaar-go/ipfs"
	"github.com/ipfs/go-ipfs/core"
	"github.com/ipfs/go-ipfs/namesys"
	"github.com/ipfs/go-ipfs/repo/fsrepo"
	"github.com/op/go-logging"
	"github.com/tyler-smith/go-bip39"
	"time"
)

var log = logging.MustGetLogger("repo")
var ErrRepoExists = errors.New("IPFS configuration file exists. Reinitializing would overwrite your keys. Use -f to force overwrite.")

func DoInit(repoRoot string, nBitsForKeypair int, testnet bool, password string, mnemonic string, creationDate time.Time, dbInit func(string, []byte, string, time.Time) error) error {
	if err := maybeCreateOBDirectories(repoRoot); err != nil {
		return err
	}

	if fsrepo.IsInitialized(repoRoot) {
		return ErrRepoExists
	}

	if err := checkWriteable(repoRoot); err != nil {
		return err
	}

	conf, err := InitConfig(repoRoot)
	if err != nil {
		return err
	}

	if mnemonic == "" {
		mnemonic, err = createMnemonic(bip39.NewEntropy, bip39.NewMnemonic)
		if err != nil {
			return err
		}
	}
	seed := bip39.NewSeed(mnemonic, "Secret Passphrase")
	fmt.Printf("Generating Ed25519 keypair...")
	identityKey, err := ipfs.IdentityKeyFromSeed(seed, nBitsForKeypair)
	if err != nil {
		return err
	}
	fmt.Printf("Done\n")

	identity, err := ipfs.IdentityFromKey(identityKey)
	if err != nil {
		return err
	}

	log.Infof("Initializing OpenBazaar node at %s\n", repoRoot)
	if err := fsrepo.Init(repoRoot, conf); err != nil {
		return err
	}
	conf.Identity = identity

	if err := addConfigExtensions(repoRoot, testnet); err != nil {
		return err
	}

	if err := dbInit(mnemonic, identityKey, password, creationDate); err != nil {
		return err
	}

	return initializeIpnsKeyspace(repoRoot, identityKey)
}

func maybeCreateOBDirectories(repoRoot string) error {
	if err := os.MkdirAll(path.Join(repoRoot, "root"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "root", "listings"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "root", "ratings"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "root", "images"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "root", "images", "tiny"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "root", "images", "small"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "root", "images", "medium"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "root", "images", "large"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "root", "images", "original"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "root", "feed"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "root", "channel"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "root", "files"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "outbox"), os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(repoRoot, "logs"), os.ModePerm); err != nil {
		return err
	}
	return nil
}

func checkWriteable(dir string) error {
	_, err := os.Stat(dir)
	if err == nil {
		// Directory exists, make sure we can write to it
		testfile := path.Join(dir, "test")
		fi, err := os.Create(testfile)
		if err != nil {
			if os.IsPermission(err) {
				return fmt.Errorf("%s is not writeable by the current user", dir)
			}
			return fmt.Errorf("Unexpected error while checking writeablility of repo root: %s", err)
		}
		fi.Close()
		return os.Remove(testfile)
	}

	if os.IsNotExist(err) {
		// Directory does not exist, check that we can create it
		return os.Mkdir(dir, 0775)
	}

	if os.IsPermission(err) {
		return fmt.Errorf("Cannot write to %s, incorrect permissions", err)
	}

	return err
}

func initializeIpnsKeyspace(repoRoot string, privKeyBytes []byte) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, err := fsrepo.Open(repoRoot)
	if err != nil { // NB: repo is owned by the node
		return err
	}
	cfg, err := r.Config()
	if err != nil {
		log.Error(err)
		return err
	}
	identity, err := ipfs.IdentityFromKey(privKeyBytes)
	if err != nil {
		return err
	}

	cfg.Identity = identity
	nd, err := core.NewNode(ctx, &core.BuildCfg{Repo: r})
	if err != nil {
		return err
	}
	defer nd.Close()

	err = nd.SetupOfflineRouting()
	if err != nil {
		return err
	}

	return namesys.InitializeKeyspace(ctx, nd.DAG, nd.Namesys, nd.Pinning, nd.PrivateKey)
}

func addConfigExtensions(repoRoot string, testnet bool) error {
	r, err := fsrepo.Open(repoRoot)
	if err != nil { // NB: repo is owned by the node
		return err
	}
	var w WalletConfig = WalletConfig{
		Type:             "spvwallet",
		MaxFee:           2000,
		FeeAPI:           "https://bitcoinfees.21.co/api/v1/fees/recommended",
		HighFeeDefault:   160,
		MediumFeeDefault: 140,
		LowFeeDefault:    120,
		TrustedPeer:      "",
	}

	var a APIConfig = APIConfig{
		Enabled:     true,
		AllowedIPs:  []string{},
		HTTPHeaders: nil,
	}

	var t TorConfig = TorConfig{}
	if err := extendConfigFile(r, "Wallet", w); err != nil {
		return err
	}
	if err := extendConfigFile(r, "Resolver", "https://resolver.onename.com/"); err != nil {
		return err
	}
	if err := extendConfigFile(r, "Crosspost-gateways", []string{"https://gateway.ob1.io/", "https://gateway.duosear.ch/"}); err != nil {
		return err
	}
	if err := extendConfigFile(r, "Dropbox-api-token", ""); err != nil {
		return err
	}
	if err := extendConfigFile(r, "JSON-API", a); err != nil {
		return err
	}
	if err := extendConfigFile(r, "Tor-config", t); err != nil {
		return err
	}
	if err := r.Close(); err != nil {
		return err
	}
	return nil
}

func createMnemonic(newEntropy func(int) ([]byte, error), newMnemonic func([]byte) (string, error)) (string, error) {
	entropy, err := newEntropy(128)
	if err != nil {
		return "", err
	}
	mnemonic, err := newMnemonic(entropy)
	if err != nil {
		return "", err
	}
	return mnemonic, nil
}
