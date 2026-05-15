package collector

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/pkg/errors"

	"github.com/prometheus/client_golang/prometheus"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type connectionMessage struct {
	DownloadTotal int64         `json:"downloadTotal"`
	UploadTotal   int64         `json:"uploadTotal"`
	Connections   []Connections `json:"connections"`
}

type Metadata struct {
	Network         string `json:"network"`
	Type            string `json:"type"`
	SourceIP        string `json:"sourceIP"`
	DestinationIP   string `json:"destinationIP"`
	SourcePort      string `json:"sourcePort"`
	DestinationPort string `json:"destinationPort"`
	Host            string `json:"host"`
	DNSMode         string `json:"dnsMode"`
	ProcessPath     string `json:"processPath"`
	SpecialProxy    string `json:"specialProxy"`
}
type Connections struct {
	ID          string    `json:"id"`
	Metadata    Metadata  `json:"metadata"`
	Upload      int       `json:"upload"`
	Download    int       `json:"download"`
	Start       time.Time `json:"start"`
	Chains      []string  `json:"chains"`
	Rule        string    `json:"rule"`
	RulePayload string    `json:"rulePayload"`
}

var (
	uploadTotal       *prometheus.GaugeVec
	downloadTotal     *prometheus.GaugeVec
	activeConnections *prometheus.GaugeVec
	policyDownload    *prometheus.CounterVec
	policyUpload      *prometheus.CounterVec
)

type Connection struct {
	connectionCache map[string]Connections
}

func (c *Connection) Name() string {
	return "connections"
}

func (c *Connection) Collect(config CollectConfig) error {
	log.Println("starting collector:", c.Name())
	ctx := context.Background()
	endpoint := fmt.Sprintf("ws://%s/connections", config.ClashHost)
	if config.ClashToken != "" {
		endpoint = fmt.Sprintf("%s?token=%s", endpoint, config.ClashToken)
	}
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		log.Fatal("failed to dial: ", err)
	}

	conn.SetReadLimit(10 * 1024 * 1024)

	defer conn.Close(websocket.StatusInternalError, "the sky is falling")
	for {
		var m connectionMessage
		err = wsjson.Read(ctx, conn, &m)
		if err != nil {
			return errors.Wrap(err, "failed to read JSON message")
		}
		uploadTotal.WithLabelValues().Set(float64(m.UploadTotal))
		downloadTotal.WithLabelValues().Set(float64(m.DownloadTotal))
		activeConnections.WithLabelValues().Set(float64(len(m.Connections)))

		activeConnectionsMap := make(map[string]interface{})
		for _, connection := range m.Connections {
			if _, ok := c.connectionCache[connection.ID]; !ok {
				c.connectionCache[connection.ID] = Connections{
					Upload:   0,
					Download: 0,
				}
			}
			policy := connection.Chains[0]
			policyDownload.WithLabelValues(policy).Add(float64(connection.Download) - float64(c.connectionCache[connection.ID].Download))
			policyUpload.WithLabelValues(policy).Add(float64(connection.Upload) - float64(c.connectionCache[connection.ID].Upload))
			c.connectionCache[connection.ID] = connection
			activeConnectionsMap[connection.ID] = nil
		}
		for id := range c.connectionCache {
			if _, ok := activeConnectionsMap[id]; !ok {
				delete(c.connectionCache, id)
			}
		}
	}
}

func init() {
	uploadTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "clash",
			Name:      "upload_bytes_total",
			Help:      "Total upload bytes",
		},
		[]string{},
	)
	downloadTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "clash",
			Name:      "download_bytes_total",
			Help:      "Total download bytes",
		},
		[]string{},
	)

	activeConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "clash",
			Name:      "active_connections",
			Help:      "Active connections",
		},
		[]string{},
	)

	policyDownload = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "clash",
			Name:      "policy_download_bytes_total",
			Help:      "Total download bytes by policy",
		},
		[]string{"policy"},
	)
	policyUpload = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "clash",
			Name:      "policy_upload_bytes_total",
			Help:      "Total upload bytes by policy",
		},
		[]string{"policy"},
	)

	prometheus.MustRegister(uploadTotal, downloadTotal, activeConnections, policyDownload, policyUpload)

	c := &Connection{connectionCache: map[string]Connections{}}
	Register(c)
}
