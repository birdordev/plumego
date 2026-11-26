package billing

import (
	"errors"
	"math/big"
	"slices"
	"strconv"
	"strings"
)

const allocationScale = 12

type allocationLine struct {
	currency, resource, service, region, category, tags string
	billed                                              string
	effective, list                                     *string
}

type allocationAmounts struct {
	billed, effective, list       big.Int
	rows                          int
	missingEffective, missingList int
}

func decimalAtoms(value string) (*big.Int, error) {
	whole, fraction, _ := strings.Cut(value, ".")
	if len(fraction) > allocationScale {
		return nil, ErrCorrupt
	}
	n, ok := new(big.Int).SetString(whole+fraction+strings.Repeat("0", allocationScale-len(fraction)), 10)
	if !ok {
		return nil, ErrCorrupt
	}
	return n, nil
}

func decimalAmount(n *big.Int) string {
	value := n.String()
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	if len(value) <= allocationScale {
		value = strings.Repeat("0", allocationScale+1-len(value)) + value
	}
	at := len(value) - allocationScale
	value = value[:at] + "." + value[at:]
	if negative {
		return "-" + value
	}
	return value
}

func addDecimal(total *big.Int, value string) error {
	n, err := decimalAtoms(value)
	if err != nil {
		return err
	}
	total.Add(total, n)
	return nil
}

func addOptional(total *big.Int, missing *int, value *string) error {
	if value == nil {
		*missing++
		return nil
	}
	return addDecimal(total, *value)
}

func (a *allocationAmounts) add(line allocationLine) error {
	a.rows++
	return errors.Join(addDecimal(&a.billed, line.billed), addOptional(&a.effective, &a.missingEffective, line.effective),
		addOptional(&a.list, &a.missingList, line.list))
}

func (rule AllocationRule) matchLine(line allocationLine) bool {
	return (rule.Resource == "" || rule.Resource == line.resource) && (rule.Service == "" || rule.Service == line.service) &&
		(rule.Region == "" || rule.Region == line.region) && (rule.Category == "" || rule.Category == line.category)
}

func allocationMatch(rules []AllocationRule, line allocationLine) (int, bool) {
	var tags map[string]string
	var read, invalid bool
	for i, rule := range rules {
		if !rule.matchLine(line) {
			continue
		}
		if len(rule.Tags) == 0 {
			return i, invalid
		}
		if !read {
			read = true
			if line.tags != "" {
				var valid bool
				tags, valid = readAllocationTags(line.tags)
				invalid = !valid
			}
		}
		matched := !invalid
		for key, value := range rule.Tags {
			if tags[key] != string(value) {
				matched = false
				break
			}
		}
		if matched {
			return i, invalid
		}
	}
	return len(rules), invalid
}

type residue struct {
	index int
	value int64
}

func splitAmount(amount *big.Int, shares []AllocationShare) []big.Int {
	result := make([]big.Int, len(shares))
	if len(shares) == 1 {
		result[0].Set(amount)
		return result
	}
	var absolute, distributed, product, remainder big.Int
	absolute.Abs(amount)
	divisor := big.NewInt(allocationWeight)
	order := make([]residue, len(shares))
	for i, share := range shares {
		product.Mul(&absolute, big.NewInt(int64(share.Weight)))
		result[i].QuoRem(&product, divisor, &remainder)
		distributed.Add(&distributed, &result[i])
		order[i] = residue{i, remainder.Int64()}
	}
	slices.SortStableFunc(order, func(a, b residue) int { return int(b.value - a.value) })
	remaining := absolute.Sub(&absolute, &distributed).Int64()
	one := big.NewInt(1)
	for _, rank := range order[:int(remaining)] {
		result[rank.index].Add(&result[rank.index], one)
	}
	if amount.Sign() < 0 {
		for i := range result {
			result[i].Neg(&result[i])
		}
	}
	return result
}

func policyDigest(encoded []byte) string {
	return digestOf(append([]byte(allocationAlgorithm+"\x00"), encoded...))
}

func amountPointer(n *big.Int, missing, rows int) *string {
	if missing != 0 || rows == 0 {
		return nil
	}
	value := decimalAmount(n)
	return &value
}

func explainAllocationPart(part, row int, err error) error {
	return errors.Join(ErrCorrupt, errors.New("allocation source part "+strconv.Itoa(part)+" row "+strconv.Itoa(row)), err)
}
