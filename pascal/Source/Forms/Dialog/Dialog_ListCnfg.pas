unit Dialog_ListCnfg;

interface

uses
  System.SysUtils, System.Types, System.UITypes, System.Classes, System.Variants,
  FMX.Types, FMX.Controls, FMX.Forms, FMX.Graphics, FMX.Dialogs;

type
  TDialogListCnfg = class(TForm)
  private
  public
  end;

var
  DialogListCnfg: TDialogListCnfg;

function ViewListCnfg(var AObjt:TObject):Integer;

implementation

{$R *.fmx}

function ViewListCnfg(var AObjt:TObject):Integer;
begin
  try
    DialogListCnfg:=TDialogListCnfg.Create(nil);
    Result:=DialogListCnfg.ShowModal;
  finally
    FreeAndNil(DialogListCnfg);
  end;
end;

end.
