package kernel

import "errors"

// Money is an amount in the currency's minor unit (cents, or whole yen), never a float.
type Money struct {
	Amount   int64
	Currency Currency
}

const maxMoneyAmount = 1_000_000_000_000

type MoneyInput struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

func (v *Validator) Money(field string, in *MoneyInput) *Money {
	if in == nil {
		return nil
	}
	currency, err := ParseCurrency(in.Currency)
	if err != nil {
		v.Add(field+".currency", err.Error())
	}
	if in.Amount < 0 || in.Amount > maxMoneyAmount {
		v.Add(field+".amount", "must be between 0 and 1000000000000 in the currency's minor unit")
	}
	return &Money{Amount: in.Amount, Currency: currency}
}

func SumMoney(items []*Money) (*Money, error) {
	var total *Money
	for _, item := range items {
		if item == nil {
			continue
		}
		if total == nil {
			total = &Money{Currency: item.Currency}
		}
		if total.Currency != item.Currency {
			return nil, errors.New("cannot sum different currencies")
		}
		total.Amount += item.Amount
	}
	return total, nil
}
