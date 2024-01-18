# sh

# patch golang asn1.go to support connecting to the Elli Connect wallboxes, which use non standard conform certificate
# this needs to be run once on each golang installation or update

# this might need to be run with sudo
GOROOT=`go env GOROOT`
BASEDIR=$(dirname $(realpath "$0"))
cat $GOROOT/src/vendor/golang.org/x/crypto/cryptobyte/asn1.go | grep -C 1 "out = true"
patch -N -t -d $GOROOT/src/vendor/golang.org/x/crypto/cryptobyte -i $BASEDIR/asn1.diff
cat $GOROOT/src/vendor/golang.org/x/crypto/cryptobyte/asn1.go | grep -C 1 "out = true"
