unit Dialog_ListUniConfig;

interface

uses
  System.SysUtils, System.Types, System.UITypes, System.Classes, System.Variants,
  FMX.Types, FMX.Controls, FMX.Forms, FMX.Graphics, FMX.Dialogs;

type
  TDialogListUniConfig = class(TForm)
  private
  public
  end;

var
  DialogListUniConfig: TDialogListUniConfig;

function ViewListUniConfig(var AObjt:TObject):Integer;

implementation

{$R *.fmx}

function ViewListUniConfig(var AObjt:TObject):Integer;
begin
  try
    DialogListUniConfig:=TDialogListUniConfig.Create(nil);
    Result:=DialogListUniConfig.ShowModal;
  finally
    FreeAndNil(DialogListUniConfig);
  end;
end;

end.
