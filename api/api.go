package api

import (
	"crypto/subtle"
	"net"
	"startup-daemon/controller"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

type headers struct {
	Host string `header:"X-Forwarded-Host"`
}

// PokeFactory returns an HTTP handler that authenticates a request, finds
// containers matching its forwarded host, and refreshes their TTL leases.
func PokeFactory(ctrl *controller.Controller, token, labelPrefix string) (poke gin.HandlerFunc) {
	hostLabel := labelPrefix + ".host"
	ttlLabel := labelPrefix + ".ttl"

	poke = func(c *gin.Context) {
		provided := c.GetHeader("Authorization")
		expected := "Bearer " + token
		if token == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			c.JSON(401, gin.H{"err": "unauthorized"})
			return
		}

		var headers headers
		if err := c.ShouldBindHeader(&headers); err != nil {
			c.JSON(400, gin.H{"err": err.Error()})
			return
		}

		if headers.Host == "" {
			c.JSON(400, gin.H{"err": "X-Forwarded-Host is required"})
			return
		}

		containers, err := ctrl.ContainerList(c, client.ContainerListOptions{
			All: true,
			Filters: client.Filters{
				"label": {hostLabel: true},
			},
		})

		if err != nil {
			c.JSON(502, gin.H{"err": err.Error()})
			return
		}

		host := normalizeHost(headers.Host)
		matched := make([]container.Summary, 0, len(containers.Items))
		for _, summary := range containers.Items {
			if normalizeHost(summary.Labels[hostLabel]) == host {
				matched = append(matched, summary)
			}
		}

		if len(matched) == 0 {
			c.JSON(404, gin.H{"err": "no container configured for host"})
			return
		}

		for _, summary := range matched {
			var ttl time.Duration
			ttlString, ok := summary.Labels[ttlLabel]
			if !ok || strings.TrimSpace(ttlString) == "" {
				c.JSON(422, gin.H{"err": "container is missing required ttl label"})
				return
			}
			ttl, err = time.ParseDuration(ttlString)
			if err != nil {
				c.JSON(422, gin.H{"err": "invalid ttl label: " + err.Error()})
				return
			}
			if ttl <= 0 || ttl > 24*time.Hour {
				c.JSON(422, gin.H{"err": "ttl must be positive and no longer than 24 hours"})
				return
			}

			if err := ctrl.Poke(c, summary, ttl); err != nil {
				if summary.State == container.StateDead {
					c.JSON(409, gin.H{"err": err.Error()})
					return
				}
				c.JSON(502, gin.H{"err": err.Error()})
				return
			}
		}

		c.JSON(200, gin.H{"msg": "ok"})
	}

	return poke
}

// normalizeHost makes host comparisons insensitive to case, port, and a final dot.
func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	return strings.TrimSuffix(host, ".")
}
