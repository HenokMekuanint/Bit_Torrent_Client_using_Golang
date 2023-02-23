package peers

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
)

type Peer struct {
	IP   net.IP
	Port uint16
}

func differentiatePeers(peersResp []byte) ([]Peer, error) {
	const peerSize = 6 
	numPeers := len(peersResp) / peerSize
	if len(peersResp)%peerSize != 0 {
		err := fmt.Errorf("peers len err")
		return nil, err
	}
	peers := make([]Peer, numPeers)
	for current_peer := 0; current_peer < numPeers; current_peer++ {
		offset := current_peer * peerSize
		peers[current_peer].IP = net.IP(peersResp[offset : offset+4])
		peers[current_peer].Port = binary.BigEndian.Uint16(peersResp[offset+4 : offset+6])
	}
	return peers, nil
}

func (p Peer) String() string {
	return net.JoinHostPort(p.IP.String(), strconv.Itoa(int(p.Port)))
}
