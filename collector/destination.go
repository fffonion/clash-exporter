package collector

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/metacubex/geo/encoding/v2raygeo"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

var (
	destinationDownloadBytesTotal *prometheus.CounterVec
	destinationUploadBytesTotal   *prometheus.CounterVec
)

var (
	geoSiteMatcher     *geoSiteMatcherImpl
	geoSiteMatcherOnce sync.Once
)

type geoSiteMatcherImpl struct {
	full   map[string]string
	suffix map[string]string
}

func newGeoSiteMatcher() *geoSiteMatcherImpl {
	// Use MetaCubeX geosite.dat - updated daily with company/organization names
	resp, err := http.Get("https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat")
	if err != nil {
		log.Println("failed to download geosite.dat:", err)
		return nil
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("failed to read geosite.dat:", err)
		return nil
	}

	list, err := v2raygeo.LoadSite(data)
	if err != nil {
		log.Println("failed to load geosite.dat:", err)
		return nil
	}

	m := &geoSiteMatcherImpl{
		full:   make(map[string]string),
		suffix: make(map[string]string),
	}
	for _, site := range list {
		code := site.GetCountryCode()
		if !isSpecificGeoSite(code) {
			continue
		}
		for _, domain := range site.GetDomain() {
			switch domain.GetType() {
			case v2raygeo.Domain_Full:
				m.full[strings.ToLower(domain.GetValue())] = code
			case v2raygeo.Domain_Domain:
				m.suffix[strings.ToLower(domain.GetValue())] = code
			}
		}
	}
	return m
}

func getGeoSiteMatcher() *geoSiteMatcherImpl {
	geoSiteMatcherOnce.Do(func() {
		geoSiteMatcher = newGeoSiteMatcher()
	})
	return geoSiteMatcher
}

func isSpecificGeoSite(code string) bool {
	code = strings.ToUpper(code)
	if code == "" {
		return false
	}
	if code == "CN" || code == "!CN" || code == "PRIVATE" {
		return false
	}
	for _, prefix := range []string{
		"CATEGORY-",
		"GEOLOCATION-",
		"GEOSITE-",
		"TLD-",
		"GEOIP-",
	} {
		if strings.HasPrefix(code, prefix) {
			return false
		}
	}
	return true
}

func (m *geoSiteMatcherImpl) lookup(host string) string {
	host = strings.ToLower(host)
	if code, ok := m.full[host]; ok && isSpecificGeoSite(code) {
		return strings.ToLower(code)
	}
	s := host
	for {
		if code, ok := m.suffix[s]; ok && isSpecificGeoSite(code) {
			return strings.ToLower(code)
		}
		i := strings.IndexByte(s, '.')
		if i < 0 || i+1 >= len(s) {
			break
		}
		s = s[i+1:]
	}
	return ""
}

func lookupGeoSite(host string) string {
	m := getGeoSiteMatcher()
	if m == nil {
		return ""
	}
	return m.lookup(host)
}

type destConnectionMessage struct {
	DownloadTotal int64         `json:"downloadTotal"`
	UploadTotal   int64         `json:"uploadTotal"`
	Connections   []Connections `json:"connections"`
}

type Destination struct {
	connectionCache map[string]Connections
}

func (d *Destination) Name() string {
	return "destination"
}

func (d *Destination) Collect(config CollectConfig) error {
	log.Println("starting collector:", d.Name())
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
		var m destConnectionMessage
		err = wsjson.Read(ctx, conn, &m)
		if err != nil {
			return errors.Wrap(err, "failed to read JSON message")
		}

		activeConnectionsMap := make(map[string]interface{})
		for _, connection := range m.Connections {
			if _, ok := d.connectionCache[connection.ID]; !ok {
				d.connectionCache[connection.ID] = Connections{
					Upload:   0,
					Download: 0,
				}
			}
			destination := connection.Metadata.Host
			if destination == "" {
				destination = connection.Metadata.DestinationIP
			}
			if !config.CollectDest {
				destination = ""
			}

			if destination != "" {
				geosite := lookupGeoSite(destination)
				if geosite != "" {
					destination = geosite
				}
			}

			destinationDownloadBytesTotal.WithLabelValues(destination).Add(float64(connection.Download) - float64(d.connectionCache[connection.ID].Download))
			destinationUploadBytesTotal.WithLabelValues(destination).Add(float64(connection.Upload) - float64(d.connectionCache[connection.ID].Upload))
			d.connectionCache[connection.ID] = connection
			activeConnectionsMap[connection.ID] = nil
		}
		for id := range d.connectionCache {
			if _, ok := activeConnectionsMap[id]; !ok {
				delete(d.connectionCache, id)
			}
		}
	}
}

func init() {
	destinationDownloadBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "clash",
			Name:      "destination_download_bytes_total",
			Help:      "Total download bytes by destination",
		},
		[]string{"destination"},
	)
	destinationUploadBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "clash",
			Name:      "destination_upload_bytes_total",
			Help:      "Total upload bytes by destination",
		},
		[]string{"destination"},
	)

	prometheus.MustRegister(destinationDownloadBytesTotal, destinationUploadBytesTotal)

	d := &Destination{connectionCache: map[string]Connections{}}
	Register(d)
}
