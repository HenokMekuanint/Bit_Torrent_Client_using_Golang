package main

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/btcsuite/btcd/wire"
)

const (
	nounce  = 0x0539a019ca550825
	minPort = 0
	maxPort = 65535

	crawlDelay = 22
	auditDelay = 22
	dnsDelay   = 57
	maxFails   = 58
	maxTo      = 250
)

const (
	dnsInvalid = iota
	dnsV4Std
	dnsV4Non
	dnsV6Std
	dnsV6Non
	maxDNSTypes
)

const (
	statusRG = iota
	statusCG
	statusWG
	statusNG
	maxStatusTypes
)

type dnsseeder struct {
	id        wire.BitcoinNet
	theList   map[string]*node
	mtx       sync.RWMutex
	dnsHost   string
	name      string
	desc      string
	initialIP string
	seeders   []string
	maxStart  []uint32
	counts    NodeCounts
	pver      uint32
	ttl       uint32
	maxSize   int
	port      uint16
}

type result struct {
	nas        []*wire.NetAddress
	msg        *crawlError
	node       string
	version    int32
	services   wire.ServiceFlag
	lastBlock  int32
	strVersion string
}

func (s *dnsseeder) initSeeder() {

	for _, aseeder := range s.seeders {
		c := 0

		if aseeder == "" {
			continue
		}
		newRRs, err := net.LookupHost(aseeder)
		if err != nil {
			log.Printf("%s: unable to do initial lookup to seeder %s %v\n", s.name, aseeder, err)
			continue
		}

		for _, ip := range newRRs {
			if newIP := net.ParseIP(ip); newIP != nil {
				if x := s.addNa(wire.NewNetAddressIPPort(newIP, s.port, 1)); x == true {
					c++
				}
			}
		}
		if config.verbose {
			log.Printf("%s: completed import of %v addresses from %s\n", s.name, c, aseeder)
		}
	}

	if len(s.theList) == 0 && s.initialIP != "" {
		if newIP := net.ParseIP(s.initialIP); newIP != nil {
			if x := s.addNa(wire.NewNetAddressIPPort(newIP, s.port, 1)); x == true {
				log.Printf("%s: crawling with initial IP %s \n", s.name, s.initialIP)
			}
		}
	}

	if len(s.theList) == 0 {
		log.Printf("%s: Error: No ip addresses from seeders so I have nothing to crawl.\n", s.name)
		for _, v := range s.seeders {
			log.Printf("%s: Seeder: %s\n", s.name, v)
		}
		log.Printf("%s: Initial IP: %s\n", s.name, s.initialIP)
	}
}

func (s *dnsseeder) runSeeder(done <-chan struct{}, wg *sync.WaitGroup) {

	defer wg.Done()

	resultsChan := make(chan *result)

	s.initSeeder()

	s.startCrawlers(resultsChan)

	auditChan := time.NewTicker(time.Minute * auditDelay).C
	crawlChan := time.NewTicker(time.Second * crawlDelay).C
	dnsChan := time.NewTicker(time.Second * dnsDelay).C

	dowhile := true
	for dowhile == true {
		select {
		case r := <-resultsChan:
			s.processResult(r)
		case <-dnsChan:
			s.loadDNS()
		case <-auditChan:
			s.auditNodes()
		case <-crawlChan:
			s.startCrawlers(resultsChan)
		case <-done:
			dowhile = false
		}
	}
	fmt.Printf("shutting down seeder: %s\n", s.name)

}

func (s *dnsseeder) startCrawlers(resultsChan chan *result) {

	s.mtx.RLock()
	defer s.mtx.RUnlock()

	tcount := uint32(len(s.theList))
	if tcount == 0 {
		if config.debug {
			log.Printf("%s - debug - startCrawlers fail: no node available\n", s.name)
		}
		return
	}

	started := make([]uint32, maxStatusTypes)
	totals := make([]uint32, maxStatusTypes)

	for _, nd := range s.theList {

		totals[nd.status]++

		if nd.crawlActive == true {
			continue
		}

		if started[nd.status] >= s.maxStart[nd.status] {
			continue
		}

		if (time.Now().Unix() - s.delay[nd.status]) <= nd.lastTry.Unix() {
			continue
		}

		nd.crawlActive = true
		nd.crawlStart = time.Now()

		go crawlNode(resultsChan, s, nd)
		started[nd.status]++
	}

	go updateNodeCounts(s, tcount, started, totals)

}

func (s *dnsseeder) processResult(r *result) {

	var nd *node

	s.mtx.Lock()
	defer s.mtx.Unlock()

	if _, ok := s.theList[r.node]; ok {
		nd = s.theList[r.node]
	} else {
		log.Printf("%s: warning - ignoring results from unknown node: %s\n", s.name, r.node)
		return
	}

	defer crawlEnd(nd)

	if r.msg != nil {
		nd.lastTry = time.Now()
		nd.connectFails++
		nd.statusStr = r.msg.Error()

		switch nd.status {
		case statusRG:
			if len(s.theList) > s.maxSize {
				nd.status = statusNG
			} else {
				if nd.rating += 25; nd.rating > 30 {
					nd.status = statusWG
				}
			}
		case statusCG:
			if nd.rating += 25; nd.rating >= 50 {
				nd.status = statusWG
			}
		case statusWG:
			if nd.rating += 15; nd.rating >= 100 {
				nd.status = statusNG
			}
		}

		if config.verbose {
			log.Printf("%s: failed crawl node: %s s:r:f: %v:%v:%v %s\n",
				s.name,
				net.JoinHostPort(nd.na.IP.String(),
					strconv.Itoa(int(nd.na.Port))),
				nd.status,
				nd.rating,
				nd.connectFails,
				nd.statusStr)
		}
		return
	}

	nd.status = statusCG
	cs := nd.lastConnect
	nd.rating = 0
	nd.connectFails = 0
	nd.lastConnect = time.Now()
	nd.lastTry = nd.lastConnect
	nd.statusStr = "ok: received remote address list"
	nd.version = r.version
	nd.services = r.services
	nd.lastBlock = r.lastBlock
	nd.strVersion = r.strVersion

	added := 0

	if len(s.theList) < s.maxSize {
		oneThird := int(float64(s.maxSize / 3))

		for _, na := range r.nas {
			if x := s.addNa(na); x == true {
				if added++; added > oneThird {
					break
				}
			}
		}
	}

	if config.verbose {
		log.Printf("%s: crawl done: node: %s s:r:f: %v:%v:%v addr: %v:%v CrawlTime: %s Last connect: %v ago\n",
			s.name,
			net.JoinHostPort(nd.na.IP.String(),
				strconv.Itoa(int(nd.na.Port))),
			nd.status,
			nd.rating,
			nd.connectFails,
			len(r.nas),
			added,
			time.Since(nd.crawlStart).String(),
			time.Since(cs).String())
	}
}

func crawlEnd(nd *node) {
	nd.crawlActive = false
}

func (s *dnsseeder) addNa(nNa *wire.NetAddress) bool {

	if len(s.theList) > s.maxSize {
		return false
	}

	k := net.JoinHostPort(nNa.IP.String(), strconv.Itoa(int(nNa.Port)))

	if _, dup := s.theList[k]; dup == true {
		return false
	}
	if nNa.Port <= minPort || nNa.Port >= maxPort {
		return false
	}

	if (time.Now().Add(-(time.Hour * 24))).After(nNa.Timestamp) {
		return false
	}

	nt := node{
		na:          nNa,
		lastConnect: time.Now(),
		version:     0,
		status:      statusRG,
		dnsType:     dnsV4Std,
	}

	if x := nt.na.IP.To4(); x == nil {
		if nNa.Port != s.port {
			nt.dnsType = dnsV6Non

			nt.nonstdIP = getNonStdIP(nt.na.IP, nt.na.Port)

		} else {
			nt.dnsType = dnsV6Std
		}
	} else {
		if nNa.Port != s.port {
			nt.dnsType = dnsV4Non

			nt.na.IP = nt.na.IP.To4()

			nt.nonstdIP = getNonStdIP(nt.na.IP, nt.na.Port)
		}
	}

	s.theList[k] = &nt

	return true
}

func getNonStdIP(rip net.IP, port uint16) net.IP {

	b := []byte{0x0, 0x0, 0x0, 0x0}
	crcAddr := crc16(rip.To4())
	b[0] = byte(crcAddr >> 8)
	b[1] = byte((crcAddr & 0xff))
	b[2] = byte(port >> 8)
	b[3] = byte(port & 0xff)

	encip := net.IPv4(b[0], b[1], b[2], b[3])
	if config.debug {
		log.Printf("debug - encode nonstd - realip: %s port: %v encip: %s crc: %x\n", rip.String(), port, encip.String(), crcAddr)
	}

	return encip
}

func crc16(bs []byte) uint16 {
	var x, crc uint16
	crc = 0xffff

	for _, v := range bs {
		x = crc>>8 ^ uint16(v)
		x ^= x >> 4
		crc = (crc << 8) ^ (x << 12) ^ (x << 5) ^ x
	}
	return crc
}

func (s *dnsseeder) auditNodes() {

	c := 0

	iAmFull := len(s.theList) > s.maxSize

	cgGoal := int(float64(float64(s.delay[statusCG]/crawlDelay)*float64(s.maxStart[statusCG])) * 0.75)
	cgCount := 0

	log.Printf("%s: Audit start. statusCG Goal: %v System Uptime: %s\n", s.name, cgGoal, time.Since(config.uptime).String())

	s.mtx.Lock()
	defer s.mtx.Unlock()

	for k, nd := range s.theList {

		if nd.crawlActive == true {
			if time.Now().Unix()-nd.crawlStart.Unix() >= 300 {
				log.Printf("warning - long running crawl > 5 minutes ====\n- %s status:rating:fails %v:%v:%v crawl start: %s last status: %s\n====\n",
					k,
					nd.status,
					nd.rating,
					nd.connectFails,
					nd.crawlStart.String(),
					nd.statusStr)
			}
		}

		if nd.status == statusNG && nd.connectFails > maxFails {
			if config.verbose {
				log.Printf("%s: purging node %s after %v failed connections\n", s.name, k, nd.connectFails)
			}

			c++

			s.theList[k] = nil
			delete(s.theList, k)
		}

		if nd.status == statusNG && iAmFull {
			if config.verbose {
				log.Printf("%s: seeder full purging node %s\n", s.name, k)
			}

			c++

			s.theList[k] = nil
			delete(s.theList, k)
		}

		if nd.status == statusCG {
			if cgCount++; cgCount > cgGoal {
				if config.verbose {
					log.Printf("%s: seeder cycle statusCG - purging node %s\n", s.name, k)
				}

				c++

				s.theList[k] = nil
				delete(s.theList, k)
			}
		}
	}
	if config.verbose {
		log.Printf("%s: Audit complete. %v nodes purged\n", s.name, c)
	}

}

func (s *dnsseeder) loadDNS() {
	updateDNS(s)
}

func requestSeederByName(name string) *dnsseeder {
	for _, s := range config.seeders {
		if s.name == name {
			return s
		}
	}
	return nil
}

func Check_Duplicate_Seeder(s *dnsseeder) (bool, error) {

	for _, v := range config.seeders {
		if v.id == s.id {
			return true, fmt.Errorf("Duplicate Magic id. Already loaded for %s so can not be used for %s", v.id, v.name, s.name)
		}
		if v.dnsHost == s.dnsHost {
			return true, fmt.Errorf("Duplicate DNS names. Already loaded %s for %s so can not be used for %s", v.dnsHost, v.name, s.name)
		}
	}
	return false, nil
}
