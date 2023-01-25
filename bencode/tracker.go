package bencode

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackpal/bencode-go"
)

type bencodeResp struct {
	Interval int    `bencode:"interval"`
	Peers    string `bencode:"peers"`
}

func (tf *TorrentFile) BuildTrackerURL(peerID [20]byte, port uint16) (string, error) {
	base, err := url.Parse(tf.Announce)
	if err != nil {
		return "", err
	}
	queryParams := url.Values{
		"info_hash":  []string{string(tf.InfoHash[:])},
		"peer_id":    []string{string(peerID[:])},
		"port":       []string{strconv.Itoa(int(port))},
		"uploaded":   []string{"0"},
		"downloaded": []string{"0"},
		"compact":    []string{"1"},
		"left":       []string{strconv.Itoa(tf.Length)},
	}
	base.RawQuery = queryParams.Encode()
	return base.String(), nil
}

func ParseResp(tf TorrentFile) (bencodeResp, error) {
	var peerID [20]byte
	_, _ = rand.Read(peerID[:])
	//Ports reserved for BitTorrent are typically 6881-6889
	url, _ := tf.BuildTrackerURL(peerID, 6889)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("get request failed")
		return bencodeResp{}, err
	}
	defer resp.Body.Close()

	pb := bencodeResp{}
	err = bencode.Unmarshal(resp.Body, &pb)
	if err != nil {
		fmt.Println("error in unmarshalling resp from tracker url")
		return bencodeResp{}, err
	}
	return pb, nil
}
