package kernel

import (
	"errors"
	"strings"
)

type Currency string

func ParseCurrency(s string) (Currency, error) {
	code := strings.ToUpper(strings.TrimSpace(s))
	if _, ok := iso4217[code]; !ok {
		return "", errors.New("must be an ISO 4217 currency code such as BRL or USD")
	}
	return Currency(code), nil
}

var iso4217 = toSet(`
AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BHD BIF BMD BND BOB BRL BSD BTN BWP BYN BZD
CAD CDF CHF CLP CNY COP CRC CUP CVE CZK DJF DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP
GMD GNF GTQ GYD HKD HNL HTG HUF IDR ILS INR IQD IRR ISK JMD JOD JPY KES KGS KHR KMF KPW KRW KWD
KYD KZT LAK LBP LKR LRD LSL LYD MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MYR MZN NAD NGN
NIO NOK NPR NZD OMR PAB PEN PGK PHP PKR PLN PYG QAR RON RSD RUB RWF SAR SBD SCR SDG SEK SGD SHP
SLE SOS SRD SSP STN SVC SYP SZL THB TJS TMT TND TOP TRY TTD TWD TZS UAH UGX USD UYU UZS VES VND
VUV WST XAF XCD XOF XPF YER ZAR ZMW ZWG
`)

func toSet(codes string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, code := range strings.Fields(codes) {
		set[code] = struct{}{}
	}
	return set
}
