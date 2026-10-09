package domain

import (
	_ "embed"
	"fmt"
	"slices"
	"sort"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// permissionsYAML es una copia del contrato C7
// (packages/schemas/permissions/v0/permissions.yaml); un test comprueba que
// no diverge.
//
//go:embed permissions.v0.yaml
var permissionsYAML []byte

// PermissionsYAML devuelve el catálogo embebido (para el test de sincronía).
func PermissionsYAML() []byte { return permissionsYAML }

// Permission es un permiso del catálogo.
type Permission struct {
	Key         string `yaml:"key"`
	Description string `yaml:"description"`
	PII         bool   `yaml:"pii"`
	Audited     bool   `yaml:"audited"`
	Reauth      bool   `yaml:"reauth"`
	RequiresMFA bool   `yaml:"requires_mfa"`
	Family      string `yaml:"-"`
}

// Role es un rol de sistema del catálogo.
type Role struct {
	ID          uuid.UUID `yaml:"-"`
	Key         string    `yaml:"key"`
	Name        string    `yaml:"name"`
	RequiresMFA bool      `yaml:"requires_mfa"`
	Permissions []string  `yaml:"permissions"`
	Family      string    `yaml:"-"`
}

// Catalog es el catálogo C7 resuelto ("*" expandido).
type Catalog struct {
	Version       string
	Permissions   []Permission
	TenantRoles   []Role
	PlatformRoles []Role
	byKey         map[string]Permission
}

type catalogFile struct {
	Version     string `yaml:"version"`
	Permissions struct {
		Tenant   []Permission `yaml:"tenant"`
		Platform []Permission `yaml:"platform"`
	} `yaml:"permissions"`
	Roles struct {
		Tenant   []Role `yaml:"tenant"`
		Platform []Role `yaml:"platform"`
	} `yaml:"roles"`
}

// roleNamespace genera IDs estables de los roles de sistema.
var roleNamespace = uuid.MustParse("6f3c2a4e-7b1d-4f59-9a3e-1c0d5e8b2a71")

// SystemRoleID es el ID fijo de un rol de sistema (igual en todas las instalaciones).
func SystemRoleID(key string) uuid.UUID { return uuid.NewSHA1(roleNamespace, []byte("role:"+key)) }

// Roles de sistema que se usan en código.
const (
	RoleTenantAdmin   = "tenant_admin"
	RolePlatformAdmin = "platform_admin"
)

// LoadCatalog interpreta el catálogo embebido.
func LoadCatalog() (*Catalog, error) { return ParseCatalog(permissionsYAML) }

// ParseCatalog interpreta un catálogo C7.
func ParseCatalog(data []byte) (*Catalog, error) {
	var f catalogFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("permissions catalog: %w", err)
	}
	c := &Catalog{Version: f.Version, byKey: map[string]Permission{}}
	var tenantKeys []string
	for _, p := range f.Permissions.Tenant {
		p.Family = "tenant"
		c.Permissions = append(c.Permissions, p)
		c.byKey[p.Key] = p
		tenantKeys = append(tenantKeys, p.Key)
	}
	for _, p := range f.Permissions.Platform {
		p.Family = "platform"
		c.Permissions = append(c.Permissions, p)
		c.byKey[p.Key] = p
	}
	resolve := func(roles []Role, family string, all []string) ([]Role, error) {
		out := make([]Role, 0, len(roles))
		for _, r := range roles {
			r.Family = family
			r.ID = SystemRoleID(r.Key)
			var perms []string
			for _, k := range r.Permissions {
				if k == "*" {
					perms = append(perms, all...)
					continue
				}
				p, ok := c.byKey[k]
				if !ok || p.Family != family {
					return nil, fmt.Errorf("permissions catalog: role %s: unknown %s permission %q", r.Key, family, k)
				}
				perms = append(perms, k)
			}
			sort.Strings(perms)
			r.Permissions = slices.Compact(perms)
			out = append(out, r)
		}
		return out, nil
	}
	var err error
	if c.TenantRoles, err = resolve(f.Roles.Tenant, "tenant", tenantKeys); err != nil {
		return nil, err
	}
	if c.PlatformRoles, err = resolve(f.Roles.Platform, "platform", nil); err != nil {
		return nil, err
	}
	return c, nil
}

// Permission devuelve un permiso por clave.
func (c *Catalog) Permission(key string) (Permission, bool) {
	p, ok := c.byKey[key]
	return p, ok
}

// PlatformRole devuelve un rol de plataforma por clave.
func (c *Catalog) PlatformRole(key string) (Role, bool) {
	for _, r := range c.PlatformRoles {
		if r.Key == key {
			return r, true
		}
	}
	return Role{}, false
}

// TenantRole devuelve un rol de tenant de sistema por clave.
func (c *Catalog) TenantRole(key string) (Role, bool) {
	for _, r := range c.TenantRoles {
		if r.Key == key {
			return r, true
		}
	}
	return Role{}, false
}

// PlatformPermissions une los permisos de los roles de plataforma dados.
func (c *Catalog) PlatformPermissions(roleKeys []string) (perms []string, requiresMFA bool) {
	for _, k := range roleKeys {
		if r, ok := c.PlatformRole(k); ok {
			perms = append(perms, r.Permissions...)
			requiresMFA = requiresMFA || r.RequiresMFA
		}
	}
	sort.Strings(perms)
	return slices.Compact(perms), requiresMFA
}
