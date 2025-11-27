package singbox

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/pasarguard/node/common"
)

type Config struct {
	raw      map[string]interface{}
	inbounds []*Inbound
}

type Inbound struct {
	tag      string
	protocol string
	raw      map[string]interface{}
	exclude  bool
	mu       sync.RWMutex
}

func NewSingBoxConfig(config string, exclude []string) (*Config, error) {
	if strings.TrimSpace(config) == "" {
		return nil, errors.New("sing-box config is empty")
	}

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(config), &raw); err != nil {
		return nil, err
	}

	inboundsAny, ok := raw["inbounds"].([]interface{})
	if !ok {
		return nil, errors.New("sing-box config doesn't have inbounds section")
	}

	excludeSet := make(map[string]struct{}, len(exclude))
	for _, tag := range exclude {
		excludeSet[tag] = struct{}{}
	}

	cfg := &Config{
		raw:      raw,
		inbounds: make([]*Inbound, 0, len(inboundsAny)),
	}

	for _, inboundVal := range inboundsAny {
		inboundMap, ok := inboundVal.(map[string]interface{})
		if !ok {
			continue
		}

		tag, _ := inboundMap["tag"].(string)
		protocol, _ := inboundMap["type"].(string)
		if protocol == "" {
			if proto, ok := inboundMap["protocol"].(string); ok {
				protocol = proto
			}
		}

		sanitizeInboundMap(inboundMap)

		inbound := &Inbound{
			tag:      tag,
			protocol: strings.ToLower(protocol),
			raw:      inboundMap,
			exclude:  false,
		}

		if _, exists := excludeSet[tag]; exists {
			inbound.exclude = true
		}

		if _, ok := inbound.raw["users"]; !ok {
			inbound.raw["users"] = []map[string]interface{}{}
		}

		cfg.inbounds = append(cfg.inbounds, inbound)
	}

	return cfg, nil
}

func (c *Config) syncUsers(users []*common.User) {
	for _, inbound := range c.inbounds {
		if inbound.exclude {
			continue
		}
		inbound.syncUsers(users)
	}
}

func (c *Config) upsertUser(user *common.User) {
	for _, inbound := range c.inbounds {
		inbound.upsertUser(user)
	}
}

func (c *Config) ToBytes() ([]byte, error) {
	return json.MarshalIndent(c.raw, "", "    ")
}

func sanitizeInboundMap(inbound map[string]interface{}) {
	if inbound == nil {
		return
	}

	if server, ok := inbound["server"]; ok {
		if _, hasListen := inbound["listen"]; !hasListen {
			inbound["listen"] = server
		}
		delete(inbound, "server")
	}

	if port, ok := inbound["server_port"]; ok {
		if _, hasListenPort := inbound["listen_port"]; !hasListenPort {
			inbound["listen_port"] = port
		}
		delete(inbound, "server_port")
	}

	delete(inbound, "packet_encoding")
}

func (i *Inbound) syncUsers(users []*common.User) {
	accounts := make([]map[string]interface{}, 0, len(users))
	for _, user := range users {
		if !shouldAttachUser(user, i.tag) {
			continue
		}
		if account := i.buildAccount(user); account != nil {
			accounts = append(accounts, account)
		}
	}
	i.setUsers(accounts)
}

func (i *Inbound) upsertUser(user *common.User) {
	if i.exclude {
		return
	}

	email := user.GetEmail()
	i.removeUser(email)

	if !shouldAttachUser(user, i.tag) {
		return
	}

	if account := i.buildAccount(user); account != nil {
		i.appendUser(account)
	}
}

func (i *Inbound) buildAccount(user *common.User) map[string]interface{} {
	proxies := user.GetProxies()
	email := user.GetEmail()

	switch i.protocol {
	case "vmess":
		if vmess := proxies.GetVmess(); vmess != nil {
			account := map[string]interface{}{
				"name": email,
				"uuid": vmess.GetId(),
			}
			return account
		}
	case "vless":
		if vless := proxies.GetVless(); vless != nil {
			account := map[string]interface{}{
				"name": email,
				"uuid": vless.GetId(),
			}
			if flow := vless.GetFlow(); flow != "" {
				account["flow"] = flow
			}
			return account
		}
	case "trojan":
		if trojan := proxies.GetTrojan(); trojan != nil {
			return map[string]interface{}{
				"name":     email,
				"password": trojan.GetPassword(),
			}
		}
	case "shadowsocks":
		if ss := proxies.GetShadowsocks(); ss != nil {
			password := common.EnsureBase64Password(ss.GetPassword(), ss.GetMethod())
			return map[string]interface{}{
				"name":     email,
				"password": password,
				"method":   ss.GetMethod(),
			}
		}
	}

	return nil
}

func (i *Inbound) setUsers(users []map[string]interface{}) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if users == nil {
		users = []map[string]interface{}{}
	}
	i.raw["users"] = users
}

func (i *Inbound) appendUser(account map[string]interface{}) {
	if account == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()

	users := i.ensureUsersLocked()
	users = append(users, account)
	i.raw["users"] = users
}

func (i *Inbound) removeUser(email string) {
	i.mu.Lock()
	defer i.mu.Unlock()

	users := i.ensureUsersLocked()
	filtered := make([]map[string]interface{}, 0, len(users))
	for _, user := range users {
		if name, _ := user["name"].(string); name == email {
			continue
		}
		filtered = append(filtered, user)
	}
	i.raw["users"] = filtered
}

func (i *Inbound) ensureUsersLocked() []map[string]interface{} {
	if users, ok := i.raw["users"].([]map[string]interface{}); ok {
		return users
	}

	rawUsers, ok := i.raw["users"].([]interface{})
	if !ok {
		users := []map[string]interface{}{}
		i.raw["users"] = users
		return users
	}

	converted := make([]map[string]interface{}, 0, len(rawUsers))
	for _, entry := range rawUsers {
		if userMap, ok := entry.(map[string]interface{}); ok {
			converted = append(converted, userMap)
		}
	}
	i.raw["users"] = converted
	return converted
}

func shouldAttachUser(user *common.User, inboundTag string) bool {
	if user == nil {
		return false
	}

	inbounds := user.GetInbounds()
	if len(inbounds) == 0 {
		return false
	}

	return slices.Contains(inbounds, inboundTag)
}
