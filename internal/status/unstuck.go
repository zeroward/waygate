package status

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrCharOnline   = errors.New("character is online")
	ErrCharNotFound = errors.New("character not found")
	ErrNoHomebind   = errors.New("no hearth bind")
)

const (
	playerFlagGhost  = 0x10
	atLoginResurrect = 0x800
)

type UnstuckResult struct {
	GUID     uint32
	Name     string
	FromMap  uint32
	FromZone uint32
	ToMap    uint32
	ToZone   uint32
	Via      string
}

func (c *Cache) Unstuck(ctx context.Context, accountID, guid uint32) (UnstuckResult, error) {
	if c.cfg.DemoMode || c.db == nil {
		return demoUnstuck(guid)
	}
	if accountID == 0 || guid == 0 {
		return UnstuckResult{}, ErrCharNotFound
	}

	ch, err := c.loadOwned(ctx, accountID, guid)
	if err != nil {
		return UnstuckResult{}, err
	}
	if ch.online != 0 {
		return UnstuckResult{}, ErrCharOnline
	}

	if c.soap != nil && c.cfg.SOAPConfigured() {
		if err := c.soap.Unstuck(ctx, ch.name); err == nil {
			_ = c.applyHomebindSQL(ctx, accountID, guid) // also clear ghost / taxi if SOAP only moved them
			bind, berr := c.homebind(ctx, guid)
			res := UnstuckResult{GUID: guid, Name: ch.name, FromMap: ch.mapID, FromZone: ch.zone, Via: "soap"}
			if berr == nil {
				res.ToMap, res.ToZone = bind.mapID, bind.zoneID
			}
			return res, nil
		}
	}

	bind, err := c.homebind(ctx, guid)
	if err != nil {
		return UnstuckResult{}, err
	}
	ok, err := c.applyHomebind(ctx, accountID, guid, bind)
	if err != nil {
		return UnstuckResult{}, err
	}
	if !ok {
		return UnstuckResult{}, ErrCharOnline
	}
	return UnstuckResult{
		GUID: guid, Name: ch.name,
		FromMap: ch.mapID, FromZone: ch.zone,
		ToMap: bind.mapID, ToZone: bind.zoneID,
		Via: "sql",
	}, nil
}

type ownedChar struct {
	name        string
	online      int
	mapID, zone uint32
}

type homebind struct {
	mapID, zoneID uint32
	x, y, z       float64
}

func (c *Cache) loadOwned(ctx context.Context, accountID, guid uint32) (ownedChar, error) {
	q := fmt.Sprintf(`
		SELECT c.`+"`name`"+`, c.`+"`online`"+`, c.`+"`map`"+`, c.`+"`zone`"+`
		FROM %s c
		WHERE c.`+"`guid`"+` = ? AND c.`+"`account`"+` = ? AND c.`+"`deleteDate`"+` IS NULL`,
		c.db.QChar("characters"),
	)
	var ch ownedChar
	err := c.db.SQL.QueryRowContext(ctx, q, guid, accountID).Scan(&ch.name, &ch.online, &ch.mapID, &ch.zone)
	if errors.Is(err, sql.ErrNoRows) {
		return ownedChar{}, ErrCharNotFound
	}
	if err != nil {
		return ownedChar{}, err
	}
	return ch, nil
}

func (c *Cache) homebind(ctx context.Context, guid uint32) (homebind, error) {
	q := fmt.Sprintf(`
		SELECT h.`+"`mapId`"+`, h.`+"`zoneId`"+`, h.`+"`posX`"+`, h.`+"`posY`"+`, h.`+"`posZ`"+`
		FROM %s h WHERE h.`+"`guid`"+` = ?`,
		c.db.QChar("character_homebind"),
	)
	var b homebind
	err := c.db.SQL.QueryRowContext(ctx, q, guid).Scan(&b.mapID, &b.zoneID, &b.x, &b.y, &b.z)
	if errors.Is(err, sql.ErrNoRows) {
		return homebind{}, ErrNoHomebind
	}
	if err != nil {
		return homebind{}, err
	}
	return b, nil
}

func (c *Cache) applyHomebind(ctx context.Context, accountID, guid uint32, b homebind) (bool, error) {
	q := fmt.Sprintf(`
		UPDATE %s SET
			`+"`position_x`"+` = ?, `+"`position_y`"+` = ?, `+"`position_z`"+` = ?, `+"`orientation`"+` = 0,
			`+"`map`"+` = ?, `+"`zone`"+` = ?,
			`+"`trans_x`"+` = 0, `+"`trans_y`"+` = 0, `+"`trans_z`"+` = 0, `+"`transguid`"+` = 0,
			`+"`taxi_path`"+` = '', `+"`cinematic`"+` = 1,
			`+"`playerFlags`"+` = `+"`playerFlags`"+` & ~?,
			`+"`at_login`"+` = `+"`at_login`"+` | ?
		WHERE `+"`guid`"+` = ? AND `+"`account`"+` = ? AND `+"`online`"+` = 0 AND `+"`deleteDate`"+` IS NULL`,
		c.db.QChar("characters"),
	)
	res, err := c.db.SQL.ExecContext(ctx, q, b.x, b.y, b.z, b.mapID, b.zoneID, playerFlagGhost, atLoginResurrect, guid, accountID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (c *Cache) applyHomebindSQL(ctx context.Context, accountID, guid uint32) error {
	bind, err := c.homebind(ctx, guid)
	if err != nil {
		return err
	}
	_, err = c.applyHomebind(ctx, accountID, guid, bind)
	return err
}

func demoUnstuck(guid uint32) (UnstuckResult, error) {
	for _, ch := range demoCharacters() {
		if ch.GUID != guid {
			continue
		}
		if ch.Online {
			return UnstuckResult{}, ErrCharOnline
		}
		return UnstuckResult{GUID: ch.GUID, Name: ch.Name, Via: "demo", ToMap: 571, ToZone: 4395}, nil
	}
	return UnstuckResult{}, ErrCharNotFound
}
