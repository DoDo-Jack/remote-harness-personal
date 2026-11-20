package main

import (
	"encoding/json"
	"math"
	"strings"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Phone     string    `json:"phone"`
	Salt        string    `json:"salt,omitempty"`
	Hash        string    `json:"hash,omitempty"`
	MemberUntil time.Time `json:"member_until,omitempty"`
	BGOpacity   int       `json:"bg_opacity,omitempty"`
	Plan        string    `json:"plan,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Devices   []string  `json:"devices"`
}

type Device struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	OS        string    `json:"os"`
	Kind      string    `json:"kind,omitempty"`
	Model     string    `json:"model,omitempty"`
	Chip      string    `json:"chip,omitempty"`
	OSVersion string    `json:"os_version,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
}

type Code struct {
	Code      string    `json:"code"`
	Days      int       `json:"days"`
	Used      bool      `json:"used"`
	UsedBy    string    `json:"used_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UsedAt    time.Time `json:"used_at,omitempty"`
}

type Order struct {
	No        string    `json:"no"`
	UserID    string    `json:"user_id"`
	Days      int       `json:"days"`
	Plan      string    `json:"plan"`
	AmountFen int       `json:"amount_fen"`
	Status    string    `json:"status"`
	TradeNo   string    `json:"trade_no,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	PaidAt    time.Time `json:"paid_at,omitempty"`
}

type State struct {
	Users   map[string]*User   `json:"users"`
	Phones  map[string]string  `json:"phones"`
	Devices map[string]*Device `json:"devices"`
	Codes   map[string]*Code   `json:"codes,omitempty"`
	Orders  map[string]*Order  `json:"orders,omitempty"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	st   *State
}

func openStore(dir string) (*Store, error) {
	s := &Store{
		path: filepath.Join(dir, "state.json"),
		st:   &State{Users: map[string]*User{}, Phones: map[string]string{}, Devices: map[string]*Device{}},
	}
	raw, err := os.ReadFile(s.path)
	if err == nil {
		if err := json.Unmarshal(raw, s.st); err != nil {
			return nil, err
		}
		if s.st.Users == nil {
			s.st.Users = map[string]*User{}
		}
		if s.st.Phones == nil {
			s.st.Phones = map[string]string{}
		}
		if s.st.Devices == nil {
			s.st.Devices = map[string]*Device{}
		}
		if s.st.Codes == nil {
			s.st.Codes = map[string]*Code{}
		}
		if s.st.Orders == nil {
			s.st.Orders = map[string]*Order{}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Store) save() {
	raw, err := json.Marshal(s.st)
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err == nil {
		_ = os.Rename(tmp, s.path)
	}
}

func (s *Store) User(id string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.st.Users[id]
}

func (s *Store) UserByPhone(phone string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if id, ok := s.st.Phones[phone]; ok {
		return s.st.Users[id]
	}
	return nil
}

func (s *Store) UserByName(name string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.st.Users {
		if u.Name == name {
			return u
		}
	}
	return nil
}

func (s *Store) CreateUserNamed(name string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.st.Users {
		if u.Name == name {
			return u
		}
	}
	u := &User{ID: "u_" + randomHex(8), Name: name, CreatedAt: time.Now().UTC()}
	s.st.Users[u.ID] = u
	s.save()
	return u
}

func (s *Store) SetPassword(userID, salt, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.st.Users[userID]; u != nil {
		u.Salt = salt
		u.Hash = hash
		s.save()
	}
}

func (s *Store) CreateUser(phone string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.st.Phones[phone]; ok {
		return s.st.Users[id]
	}
	u := &User{ID: "u_" + randomHex(8), Phone: phone, CreatedAt: time.Now().UTC()}
	s.st.Users[u.ID] = u
	s.st.Phones[phone] = u.ID
	s.save()
	return u
}

func (s *Store) AddDevice(userID, name, osName, kind, model, chip, osVer string) *Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.st.Users[userID]
	if u == nil {
		return nil
	}
	d := &Device{
		ID: "d_" + randomHex(8), UserID: userID, Name: name, OS: osName,
		Kind: kind, Model: model, Chip: chip, OSVersion: osVer,
		CreatedAt: time.Now().UTC(), LastSeen: time.Now().UTC(),
	}
	s.st.Devices[d.ID] = d
	u.Devices = append(u.Devices, d.ID)
	s.save()
	return d
}

func (s *Store) RemoveDevice(userID, deviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.st.Devices[deviceID]
	if d == nil || d.UserID != userID {
		return false
	}
	delete(s.st.Devices, deviceID)
	if u := s.st.Users[userID]; u != nil {
		for i, id := range u.Devices {
			if id == deviceID {
				u.Devices = append(u.Devices[:i], u.Devices[i+1:]...)
				break
			}
		}
	}
	s.save()
	return true
}

func (s *Store) Device(id string) *Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.st.Devices[id]
}

func (s *Store) DevicesOf(userID string) []*Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u := s.st.Users[userID]
	if u == nil {
		return nil
	}
	out := make([]*Device, 0, len(u.Devices))
	for _, id := range u.Devices {
		if d := s.st.Devices[id]; d != nil {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) TouchDevice(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d := s.st.Devices[id]; d != nil {
		d.LastSeen = time.Now().UTC()
		s.save()
	}
}

func (s *Store) UpdateDeviceMeta(id, name, kind, model, chip, osVer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.st.Devices[id]
	if d == nil {
		return
	}
	if name != "" {
		d.Name = name
	}
	if kind != "" {
		d.Kind = kind
	}
	if model != "" {
		d.Model = model
	}
	if chip != "" {
		d.Chip = chip
	}
	if osVer != "" {
		d.OSVersion = osVer
	}
	d.LastSeen = time.Now().UTC()
	s.save()
}

/* ───────────── 会员 ───────────── */

func (s *Store) MemberDaysLeft(userID string) (int, time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u := s.st.Users[userID]
	if u == nil || u.MemberUntil.Before(time.Now()) {
		return 0, time.Time{}, false
	}
	days := int(math.Ceil(time.Until(u.MemberUntil).Hours() / 24))
	if days < 0 {
		days = 0
	}
	return days, u.MemberUntil, true
}

func (s *Store) ExtendMember(userID string, days int, plan string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.st.Users[userID]
	if u == nil {
		return time.Time{}, false
	}
	base := time.Now()
	if u.MemberUntil.After(base) {
		base = u.MemberUntil
	}
	u.MemberUntil = base.Add(time.Duration(days) * 24 * time.Hour)
	u.Plan = plan
	s.save()
	return u.MemberUntil, true
}

func (s *Store) NewCodes(n, days int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Codes == nil {
		s.st.Codes = map[string]*Code{}
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var code string
		for {
			code = "DSH-" + strings.ToUpper(randomHex(2)) + "-" + strings.ToUpper(randomHex(2))
			if _, exists := s.st.Codes[code]; !exists {
				break
			}
		}
		s.st.Codes[code] = &Code{Code: code, Days: days, CreatedAt: time.Now().UTC()}
		out = append(out, code)
	}
	s.save()
	return out
}

func (s *Store) Redeem(code, userID string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.st.Codes[strings.ToUpper(strings.TrimSpace(code))]
	if c == nil || c.Used {
		return 0, false
	}
	c.Used = true
	c.UsedBy = userID
	c.UsedAt = time.Now().UTC()
	s.save()
	return c.Days, true
}

/* ───────────── 订单 ───────────── */

func (s *Store) AddOrder(o *Order) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Orders == nil {
		s.st.Orders = map[string]*Order{}
	}
	o.Status = "pending"
	o.CreatedAt = time.Now().UTC()
	s.st.Orders[o.No] = o
	s.save()
}

func (s *Store) Order(no string) *Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.st.Orders[no]
}

func (s *Store) MarkOrderPaid(no, tradeNo string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.st.Orders[no]
	if o == nil || o.Status == "paid" {
		return false
	}
	o.Status = "paid"
	o.TradeNo = tradeNo
	o.PaidAt = time.Now().UTC()
	s.save()
	return true
}

func (s *Store) SetBGOpacity(userID string, v int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.st.Users[userID]; u != nil {
		u.BGOpacity = v
		s.save()
	}
}

// 网页登录记录：同一个用户 + 同一种设备只保留一条，掉线后仍然存在
func (s *Store) EnsureWebDevice(userID, name string) *Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.st.Users[userID]
	if u == nil {
		return nil
	}
	now := time.Now()
	for _, id := range u.Devices {
		if d := s.st.Devices[id]; d != nil && d.Kind == "web" && d.Name == name {
			if now.Sub(d.LastSeen) > 45*time.Second {
				d.LastSeen = now
				s.save()
			}
			return d
		}
	}
	d := &Device{
		ID: "w_" + randomHex(8), UserID: userID, Name: name,
		OS: "web", OSVersion: "浏览器", Kind: "web",
		CreatedAt: now, LastSeen: now,
	}
	s.st.Devices[d.ID] = d
	u.Devices = append(u.Devices, d.ID)
	s.save()
	return d
}

// 个人版：只用来确保本地那一个用户存在
func (s *Store) Users() map[string]*User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.st.Users
}
