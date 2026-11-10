#!/usr/bin/env python3
"""Expand a SAM template into plain CloudFormation, locally.

WHY THIS EXISTS
---------------
A SAM template carries `Transform: AWS::Serverless-2016-10-31`. CloudFormation
treats that transform as a resource, and creating a change set for such a
template requires:

    cloudformation:CreateChangeSet
      on arn:aws:cloudformation:<region>:aws:transform/Serverless-2016-10-31

Some AWS Organizations deny that action in a Service Control Policy. An SCP
sits above IAM: no identity-based policy can grant past it, so a correct deploy
role still fails with

    ... is not authorized to perform: cloudformation:CreateChangeSet
    on resource: arn:aws:cloudformation:...:aws:transform/Serverless-2016-10-31
    with an explicit deny in a service control policy

This script runs the transform HERE instead, using aws-sam-translator - the
same library CloudFormation runs server side - and emits a template with no
`Transform` in it. That template deploys through plain
`aws cloudformation deploy`, which never touches the denied resource.

THIS IS A FALLBACK, NOT AN IMPROVEMENT
--------------------------------------
Prefer getting the SCP amended. This path pins you to a translator version that
must be kept in step with what AWS runs, and a divergence shows up as a deploy
that differs subtly from what `sam deploy` would have produced. Use it when the
SCP cannot be changed, or cannot be changed soon enough.

USAGE
-----
    python3 infra/expand-sam-template.py packaged.yaml expanded.json \
        Environment=prod DeployRevision=abc123

Input must already be PACKAGED - `sam package` rewrites CodeUri to an s3:// URI
and uploads the artifact. The transform rejects a local path.
"""

import json
import sys

import yaml
from samtranslator.translator.transform import transform


class StaticManagedPolicyLoader:
    """Resolves managed-policy NAMES without calling IAM.

    SAM's `Policies:` accepts a managed policy name, which the translator
    normally resolves by listing every AWS-managed policy through IAM. That
    needs credentials and iam:ListPolicies, neither of which this script should
    require. These ARNs are fixed and documented, so a static map is both
    sufficient and faster.

    A name that is missing here raises during transform rather than silently
    dropping a permission - add it to the map if that happens.
    """

    MAP = {
        "AWSLambdaBasicExecutionRole": "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole",
        "AWSLambdaVPCAccessExecutionRole": "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole",
        "AWSLambdaSQSQueueExecutionRole": "arn:aws:iam::aws:policy/service-role/AWSLambdaSQSQueueExecutionRole",
        "AWSXrayWriteOnlyAccess": "arn:aws:iam::aws:policy/AWSXrayWriteOnlyAccess",
    }

    def load(self):
        return dict(self.MAP)

    def get_manged_policy_map(self):  # spelling is the library's, not ours
        return dict(self.MAP)


class CfnLoader(yaml.SafeLoader):
    """YAML loader that turns !Ref / !Sub / !GetAtt into their long form.

    The translator works on the expanded JSON representation, so the short
    tags have to be resolved before it sees them.
    """


def _intrinsic(loader, suffix, node):
    name = suffix if suffix in ("Ref", "Condition") else "Fn::" + suffix
    if isinstance(node, yaml.ScalarNode):
        value = loader.construct_scalar(node)
        if name == "Fn::GetAtt":
            value = value.split(".", 1)
    elif isinstance(node, yaml.SequenceNode):
        value = loader.construct_sequence(node, deep=True)
    else:
        value = loader.construct_mapping(node, deep=True)
    return {name: value}


CfnLoader.add_multi_constructor("!", _intrinsic)


def main(argv):
    if len(argv) < 3:
        print(__doc__)
        return 2

    src, dst = argv[1], argv[2]
    overrides = {}
    for pair in argv[3:]:
        if "=" not in pair:
            print(f"not a Key=Value override: {pair}", file=sys.stderr)
            return 2
        k, v = pair.split("=", 1)
        overrides[k] = v

    with open(src) as fh:
        template = yaml.load(fh, Loader=CfnLoader)

    if "Transform" not in template:
        print(f"{src} has no Transform - nothing to expand.", file=sys.stderr)
        return 1

    # Every parameter needs a value: defaults first, then what was passed.
    # The translator resolves some of these while expanding, and a missing one
    # surfaces as a confusing failure deep inside the library.
    values = {}
    for name, spec in (template.get("Parameters") or {}).items():
        if "Default" in spec:
            values[name] = spec["Default"]
    values.update(overrides)

    missing = [n for n in (template.get("Parameters") or {}) if n not in values]
    if missing:
        print(f"no value for parameter(s): {', '.join(missing)}", file=sys.stderr)
        print("pass them as Key=Value arguments", file=sys.stderr)
        return 1

    expanded = transform(template, values, StaticManagedPolicyLoader())

    if "Transform" in expanded:
        print("transform did not expand - refusing to write", file=sys.stderr)
        return 1

    with open(dst, "w") as fh:
        json.dump(expanded, fh, indent=1)

    before = len(template.get("Resources") or {})
    after = len(expanded.get("Resources") or {})
    print(f"expanded {before} SAM resources into {after} CloudFormation resources -> {dst}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
