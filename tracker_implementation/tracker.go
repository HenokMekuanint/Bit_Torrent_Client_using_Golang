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

const (
	DefaultPort = 6889
)

func (tf *TorrentFile) BuildTrackerURL(peerID [20]byte, port uint16) (string, error) {
	base, err := url.Parse(tf.Announce)
	if err != nil {
		return "", fmt.Errorf("failed to parse announce URL: %w", err)
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

	url, err := tf.BuildTrackerURL(peerID, DefaultPort)
	if err != nil {
		return bencodeResp{}, fmt.Errorf("failed to build tracker URL: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return bencodeResp{}, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return bencodeResp{}, fmt.Errorf("failed to perform HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return bencodeResp{}, fmt.Errorf("unexpected HTTP status code: %d", resp.StatusCode)
	}

	pb := bencodeResp{}
	err = bencode.Unmarshal(resp.Body, &pb)
	if err != nil {
		fmt.Println("error in unmarshalling resp from tracker url")
		return bencodeResp{}, err
	}
	return pb, nil
}
