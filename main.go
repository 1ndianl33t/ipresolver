package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/OWASP/Amass/v3/net/http"
	amasshttp "github.com/OWASP/Amass/v3/net/http"
	"github.com/OWASP/Amass/v3/requests"
	"github.com/OWASP/Amass/v3/resolvers"
	"github.com/OWASP/Amass/v3/stringset"
)

const publicDNSResolverBaseURL = "https://public-dns.info/nameserver/"

var (
	inputFile    string
	threads      int
	resolverFile string
	onlyIp       bool
	PublicDNS    bool
)

func main() {
	flag.StringVar(&inputFile, "i", "", "Domains list")
	flag.IntVar(&threads, "t", 5, "Threads to run")
	flag.StringVar(&resolverFile, "r", "", "Resolver file (Format: ip:port)")
	flag.BoolVar(&onlyIp, "only-ip", false, "Output only IP Addresses")
	flag.BoolVar(&PublicDNS, "public-dns", false, "Output only IP Addresses")
	flag.Parse()

	if inputFile == "" {
		fmt.Println("Please check your input file.")
		os.Exit(0)
	}

	var resolversList []string

	if PublicDNS {
		cc := "us"
		if result := http.ClientCountryCode(); result != "" {
			cc = result
		}
		url := publicDNSResolverBaseURL + cc + ".txt"
		if resolvers, err := getWordlistByURL(url); err == nil && len(resolvers) >= 50 {
			resolversList = stringset.Deduplicate(resolvers)
		} else if cc != "us" {
			url = publicDNSResolverBaseURL + "us.txt"

			if resolvers, err = getWordlistByURL(url); err == nil {
				resolversList = stringset.Deduplicate(resolvers)
			}
		}
	} else if resolverFile != "" {
		rf, err := os.Open(resolverFile)
		if err != nil {
			panic(err)
		}
		defer rf.Close()
		rs := bufio.NewScanner(rf)
		for rs.Scan() {
			resolversList = append(resolversList, rs.Text())
		}
	} else {
		resolversList = []string{
			"1.1.1.1:53",     // Cloudflare
			"8.8.8.8:53",     // Google
			"64.6.64.6:53",   // Verisign
			"77.88.8.8:53",   // Yandex.DNS
			"74.82.42.42:53", // Hurricane Electric
			"1.0.0.1:53",     // Cloudflare Secondary
			"8.8.4.4:53",     // Google Secondary
			"77.88.8.1:53",   // Yandex.DNS Secondary
		}
	}
	f, err := os.Open(inputFile)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	pool := resolvers.SetupResolverPool(resolversList, false, false, nil)
	if pool == nil {
		fmt.Println("Failed to init pool")
		os.Exit(0)
	}

	var wg sync.WaitGroup
	jobChan := make(chan string, threads*2)
	ctx := context.Background()
	defer ctx.Done()

	var answers []requests.DNSAnswer
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for domain := range jobChan {
				var ans []requests.DNSAnswer
				if a, _, err := pool.Resolve(ctx, domain, "A", resolvers.PriorityHigh); err == nil {
					if a != nil && len(a) > 0 {
						ans = append(ans, a...)
					}
				}
				answers = append(answers, ans...)
			}
		}()
	}

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		jobChan <- strings.ToLower(sc.Text())
	}
	close(jobChan)
	wg.Wait()

	var rawResultsList []string
	for _, ans := range answers {
		netIP := net.ParseIP(ans.Data)
		if netIP.IsGlobalUnicast() {
			if onlyIp {
				rawResultsList = append(rawResultsList, netIP.String())
			} else {
				rawResultsList = append(rawResultsList, fmt.Sprintf("%s,%s", ans.Name, netIP.String()))

			}
		}
	}

	// Print removed duplicated list
	for _, r := range removeDuplicated(rawResultsList) {
		fmt.Println(r)
	}
}

func removeDuplicated(ips []string) []string {
	seen := make(map[string]bool)
	uniqList := []string{}
	for _, ip := range ips {
		if _, ok := seen[ip]; !ok {
			seen[ip] = true
			uniqList = append(uniqList, ip)
		}
	}
	return uniqList
}

func getWordlistByURL(url string) ([]string, error) {
	page, err := amasshttp.RequestWebPage(url, nil, nil, "", "")
	if err != nil {
		return nil, fmt.Errorf("Failed to obtain the wordlist at %s: %v", url, err)
	}
	return getWordList(strings.NewReader(page))
}

func getWordList(reader io.Reader) ([]string, error) {
	var words []string

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		// Get the next word in the list
		w := strings.TrimSpace(scanner.Text())
		if err := scanner.Err(); err == nil && w != "" {
			words = append(words, w)
		}
	}
	return stringset.Deduplicate(words), nil
}
