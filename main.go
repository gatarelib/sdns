package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/miekg/dns"
	"github.com/semihalev/log"
	"github.com/semihalev/sdns/cache"
	"github.com/yl2chen/cidranger"
)

var (
	// Config is the global configuration
	Config config

	// Version returns the build version of sdns, this should be incremented every new release
	Version = "0.2.2"

	// ConfigVersion returns the version of sdns, this should be incremented every time the config changes so sdns presents a warning
	ConfigVersion = "0.2.1"

	// ConfigPath returns the configuration path
	ConfigPath = flag.String("config", "sdns.toml", "location of the config file, if not found it will be generated")

	// LocalIPs returns list of local ip addresses
	LocalIPs []string

	// AccessList returns created CIDR rangers
	AccessList cidranger.Ranger

	// BlockList returns BlockCache
	BlockList = cache.NewBlockCache()
)

func init() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS]\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "OPTIONS:")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "USAGE:")
		fmt.Fprintln(os.Stderr, "./sdns -config=sdns.toml")
		fmt.Fprintln(os.Stderr, "")
	}
}

func configSetup(test bool) {
	if err := LoadConfig(*ConfigPath); err != nil {
		log.Crit("Config loading failed", "error", err.Error())
	}

	if test {
		Config.Bind = ":0"
		Config.BindTLS = ""
		Config.BindDOH = ""
		Config.API = "127.0.0.1:11111"
		Config.LogLevel = "crit"
		Config.Timeout.Duration = time.Second
	}

	lvl, err := log.LvlFromString(Config.LogLevel)
	if err != nil {
		log.Crit("Log verbosity level unknown")
	}

	log.Root().SetHandler(log.LvlFilterHandler(lvl, log.StdoutHandler))

	if len(Config.RootServers) > 0 {
		rootservers = &cache.AuthServers{}
		for _, s := range Config.RootServers {
			rootservers.List = append(rootservers.List, cache.NewAuthServer(s))
		}
	}

	if len(Config.Root6Servers) > 0 {
		root6servers = &cache.AuthServers{}
		for _, s := range Config.Root6Servers {
			root6servers.List = append(root6servers.List, cache.NewAuthServer(s))
		}
	}

	if len(Config.FallbackServers) > 0 {
		fallbackservers = &cache.AuthServers{}
		for _, s := range Config.FallbackServers {
			fallbackservers.List = append(fallbackservers.List, cache.NewAuthServer(s))
		}
	}

	if len(Config.RootKeys) > 0 {
		rootkeys = []dns.RR{}
		for _, k := range Config.RootKeys {
			rr, err := dns.NewRR(k)
			if err != nil {
				log.Crit("Root keys invalid", "error", err.Error())
			}
			rootkeys = append(rootkeys, rr)
		}
	}

	if Config.Timeout.Duration < 250*time.Millisecond {
		Config.Timeout.Duration = 250 * time.Millisecond
	}

	if Config.ConnectTimeout.Duration < 250*time.Millisecond {
		Config.ConnectTimeout.Duration = 250 * time.Millisecond
	}

	if Config.CacheSize < 1024 {
		Config.CacheSize = 1024
	}
}

func fetchBlocklists() {
	timer := time.NewTimer(time.Second)

	select {
	case <-timer.C:
		if err := updateBlocklists(Config.BlockListDir); err != nil {
			log.Error("Update blocklists failed", "error", err.Error())
		}

		if err := readBlocklists(Config.BlockListDir); err != nil {
			log.Error("Read blocklists failed", "dir", Config.BlockListDir, "error", err.Error())
		}
	}
}

func start() {
	var err error

	LocalIPs, err = findLocalIPAddresses()
	if err != nil {
		log.Crit("Local ip addresses failed", "error", err.Error())
	}

	AccessList = cidranger.NewPCTrieRanger()
	for _, cidr := range Config.AccessList {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			log.Crit("Access list parse cidr failed", "error", err.Error())
		}

		err = AccessList.Insert(cidranger.NewBasicRangerEntry(*ipnet))
		if err != nil {
			log.Crit("Access list insert cidr failed", "error", err.Error())
		}
	}

	server := &Server{
		host:           Config.Bind,
		tlsHost:        Config.BindTLS,
		dohHost:        Config.BindDOH,
		tlsCertificate: Config.TLSCertificate,
		tlsPrivateKey:  Config.TLSPrivateKey,
		rTimeout:       5 * time.Second,
		wTimeout:       5 * time.Second,
	}

	api := &API{
		host: Config.API,
	}

	server.Run()

	api.Run()

	go fetchBlocklists()
}

func main() {
	flag.Parse()

	log.Info("Starting sdns...", "version", Version)

	configSetup(false)
	start()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	<-c

	log.Info("Stopping sdns...")
}
