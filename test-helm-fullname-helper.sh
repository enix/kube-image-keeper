#!/bin/bash

function test() {
    local expected="$2"
    local expected2='fullnameOverride'
    output=$(helm template helm/kube-image-keeper/ -s templates/test-templating.yaml $1 | egrep '^kind:' | cut -c 7-)
    output2=$(helm template helm/kube-image-keeper/ -s templates/test-templating.yaml $1 --set fullnameOverride=fullnameOverride | egrep '^kind:'| cut -c 7-)

    echo "args: $1"
    if [[ $expected == $output ]]; then
        echo OK
    else
        echo -e "\033[0;31mKO: '$output' != '$expected'\033[0m"
    fi

    if [[ $expected2 == $output2 ]]; then
        echo OK2
    else
        echo -e "\033[0;31mKO2: '$output2' != '$expected2'\033[0m"
    fi
    echo "----------"
}

# has to be compatible with https://pkg.go.dev/helm.sh/helm/v3/pkg/releaseutil#SimpleHead
echo """
kind: {{ include \"kube-image-keeper.fullname\" . }}
""" > helm/kube-image-keeper/templates/test-templating.yaml

# basic
test ''                                             'release-name-kuik'
test '--name-template foo'                          'foo-kuik'
test '--set nameOverride=bar'                       'release-name-bar'
test '--name-template foo --set nameOverride=bar'   'foo-bar'
test '--name-template foo --set nameOverride=foo'   'foo'

# replace kube-image-keeper by kuik
test '--name-template kube-image-keeper'            'kuik'
test '--name-template kuik'                         'kuik'
## with -release postfix
test '--name-template kuik-release'                 'kuik-release'
test '--name-template kube-image-keeper-release'    'kuik-release'
## with one missing letter
test '--name-template kui'                          'kui-kuik'
test '--name-template kube-image-keepe'             'kube-image-keepe-kuik'

# merge duplicate names
test '--name-template kuik              --set nameOverride=kuik'                'kuik'
test '--name-template kube-image-keeper --set nameOverride=kuik'                'kuik'
## nameOverride should be taken in account as is and not be replaced
test '--name-template kuik              --set nameOverride=kube-image-keeper'   'kuik-kube-image-keeper'
test '--name-template kube-image-keeper --set nameOverride=kube-image-keeper'   'kuik-kube-image-keeper'

rm -f helm/kube-image-keeper/templates/test-templating.yaml
